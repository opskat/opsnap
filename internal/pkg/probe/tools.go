package probe

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/l10n"
)

// toolsDirValue 配置项 tools.dir 的当前值，通过 SetToolsDir 设置；未配置时为空，只在 PATH 中查找
var toolsDirValue atomic.Value

func init() { toolsDirValue.Store("") }

// SetToolsDir 设置主控端工具目录（配置项 tools.dir，见 configs/config.example.yaml）：
// 其中的版本子目录按服务端版本选用（见 ResolveTool）；没有版本子目录时工具先在 PATH 中查找，找不到时在这里继续找
func SetToolsDir(dir string) { toolsDirValue.Store(dir) }

func currentToolsDir() string {
	v, _ := toolsDirValue.Load().(string)
	return v
}

// ToolPath 先在 PATH 中查找 name，PATH 中找不到时在 tools.dir 中查找；找到时返回可执行文件的路径。
// 这是没有版本子目录时 ResolveTool 的查找顺序
func ToolPath(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	dir := currentToolsDir()
	if dir == "" {
		return "", false
	}
	p := filepath.Join(dir, name)
	return p, isExecutable(p)
}

// versionRe 匹配文本中第一个 x.y[.z] 形式的版本号
var versionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// distribRe 旧版 MySQL 客户端的 --version 输出（如 "mysqldump  Ver 10.13 Distrib 5.7.44"）中客户端的版本号
var distribRe = regexp.MustCompile(`Distrib\s+((\d+)\.(\d+)(?:\.\d+)?)`)

// versionText 工具 --version 输出中的版本号原文（有 Distrib 时取其后的客户端版本），如 8.4.6、16.10
func versionText(raw string) string {
	if m := distribRe.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	return versionRe.FindString(raw)
}

// ParseMajorMinor 解析版本号文本（如服务端版本、工具 --version 输出）中的主次版本号，能力探测与导出共用；
// 有 Distrib 时取它之后的版本号，Ver 之后的只是工具自身的版本
func ParseMajorMinor(text string) (major, minor int, ok bool) {
	var m []string
	if d := distribRe.FindStringSubmatch(text); d != nil {
		m = d[1:]
	} else if m = versionRe.FindStringSubmatch(text); m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

// toolStatus 主控端工具的查找与版本结果
type toolStatus struct {
	// Found 是否找到
	Found bool
	// Tool 选用的工具；Name、Path 在 Found 时有效，版本仅当 Found 且 Err 为空时有效
	Tool
	// Err 找到了但无法确定版本时的原因
	Err error
}

// lookupTool 按服务端版本选用 name（见 ResolveTool）并读取版本
func lookupTool(ctx context.Context, name, serverVersion string) toolStatus {
	t, found, err := ResolveTool(ctx, name, serverVersion)
	return toolStatus{Found: found, Tool: t, Err: err}
}

// ToolVersion 执行 `<path> --version` 并解析版本号（有 Distrib 时取其后的客户端版本），raw 为原始输出
func ToolVersion(ctx context.Context, path string) (major, minor int, raw string, err error) {
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return 0, 0, "", err
	}
	raw = strings.TrimSpace(string(out))
	major, minor, ok := ParseMajorMinor(raw)
	if !ok {
		return 0, 0, raw, l10n.Errorf(code.ProbeVersionUnrecognized, raw)
	}
	return major, minor, raw, nil
}

// Tool 选用的主控端工具
type Tool struct {
	// Name 可执行文件名；MariaDB 服务端从 mariadb/ 子目录选用时可能是 mariadb-dump
	Name string
	// Path 可执行文件的路径
	Path string
	// Version --version 中的版本号原文（如 8.4.6），Major、Minor 为其主次版本号
	Version      string
	Major, Minor int
	// Raw --version 的原始输出
	Raw string
}

// 版本子目录的名称约定（spec「按服务端版本选择导出工具」）：MySQL 为 mysql-<主>.<次>，PostgreSQL 为 postgresql-<主>
var (
	mysqlDirRe    = regexp.MustCompile(`^mysql-(\d+)\.(\d+)$`)
	postgresDirRe = regexp.MustCompile(`^postgresql-(\d+)$`)
)

// mariadbDir MariaDB 服务端使用的子目录，其中的工具依次找 mariadbDumpNames
const mariadbDir = "mariadb"

var mariadbDumpNames = []string{"mariadb-dump", "mysqldump"}

// ResolveTool 按服务端版本选用导出工具 name（mysqldump、pg_dump、pg_dumpall），能力探测与导出共用：
//   - tools.dir 下有该工具的版本子目录（<子目录>/bin/<name>）时，选不低于服务端版本的最低版本
//     （MySQL 按主次版本、PostgreSQL 按主版本比较）；都低于服务端或服务端版本未知时选最高版本；
//   - 服务端是 MariaDB 时，mysqldump 先用 mariadb/bin 下的 mariadb-dump 或 mysqldump，没有时按 MySQL 的规则；
//   - 没有版本子目录时与原来相同，见 ToolPath。
//
// 找不到时 found 为 false；找到但读不出版本时返回 err，此时 Tool 的 Name 与 Path 有效
func ResolveTool(ctx context.Context, name, serverVersion string) (tool Tool, found bool, err error) {
	path, ok := versionedToolPath(name, serverVersion)
	if !ok {
		path, ok = ToolPath(name)
	}
	if !ok {
		return Tool{Name: name}, false, nil
	}
	tool = Tool{Name: filepath.Base(path), Path: path}
	if tool.Major, tool.Minor, tool.Raw, err = ToolVersion(ctx, path); err != nil {
		return tool, true, err
	}
	tool.Version = versionText(tool.Raw)
	return tool, true, nil
}

// versionedToolPath 在 tools.dir 的版本子目录中按服务端版本选用 name；没有含该工具的版本子目录时 ok 为 false
func versionedToolPath(name, serverVersion string) (string, bool) {
	dir := currentToolsDir()
	if dir == "" {
		return "", false
	}
	var dirRe *regexp.Regexp
	switch name {
	case "mysqldump":
		if strings.Contains(serverVersion, "MariaDB") {
			for _, n := range mariadbDumpNames {
				if p := filepath.Join(dir, mariadbDir, "bin", n); isExecutable(p) {
					return p, true
				}
			}
		}
		dirRe = mysqlDirRe
	case "pg_dump", "pg_dumpall":
		dirRe = postgresDirRe
	default:
		return "", false
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var server [2]int
	var known bool
	server[0], server[1], known = ParseMajorMinor(serverVersion)
	if dirRe == postgresDirRe {
		server[1] = 0 // PostgreSQL 只比较主版本，子目录名中的次版本为 0
	}
	var best, highest string
	var bestV, highestV [2]int
	for _, e := range ents {
		m := dirRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		p := filepath.Join(dir, e.Name(), "bin", name)
		if !isExecutable(p) {
			continue
		}
		var v [2]int
		v[0], _ = strconv.Atoi(m[1])
		if len(m) > 2 {
			v[1], _ = strconv.Atoi(m[2])
		}
		if highest == "" || versionLess(highestV, v) {
			highest, highestV = p, v
		}
		if known && !versionLess(v, server) && (best == "" || versionLess(v, bestV)) {
			best, bestV = p, v
		}
	}
	if best != "" {
		return best, true
	}
	return highest, highest != ""
}

// versionLess 主次版本 a 低于 b
func versionLess(a, b [2]int) bool { return a[0] < b[0] || a[0] == b[0] && a[1] < b[1] }

// isExecutable p 是可执行的普通文件
func isExecutable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}
