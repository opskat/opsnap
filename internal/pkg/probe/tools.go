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
// mysqldump、pg_dump 等工具先在 PATH 中查找，PATH 中找不到时在这里继续找
func SetToolsDir(dir string) { toolsDirValue.Store(dir) }

func currentToolsDir() string {
	v, _ := toolsDirValue.Load().(string)
	return v
}

// ToolPath 先在 PATH 中查找 name，PATH 中找不到时在 tools.dir 中查找；找到时返回可执行文件的路径。
// 探测与备份执行使用同一查找顺序
func ToolPath(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	dir := currentToolsDir()
	if dir == "" {
		return "", false
	}
	p := filepath.Join(dir, name)
	info, err := os.Stat(p)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return "", false
	}
	return p, true
}

// versionRe 匹配文本中第一个 x.y[.z] 形式的版本号
var versionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// distribRe 旧版 MySQL 客户端的 --version 输出（如 "mysqldump  Ver 10.13 Distrib 5.7.44"）中客户端的版本号
var distribRe = regexp.MustCompile(`Distrib\s+(\d+)\.(\d+)`)

// ParseMajorMinor 解析版本号文本（如服务端版本、工具 --version 输出）中的主次版本号，能力探测与导出共用；
// 有 Distrib 时取它之后的版本号，Ver 之后的只是工具自身的版本
func ParseMajorMinor(text string) (major, minor int, ok bool) {
	m := distribRe.FindStringSubmatch(text)
	if m == nil {
		m = versionRe.FindStringSubmatch(text)
	}
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

// toolStatus 主控端工具的查找与版本结果
type toolStatus struct {
	// Found 是否在 PATH 或 tools.dir 中找到
	Found bool
	// Major、Minor 解析出的版本号，仅当 Found 且 Err 为空时有效
	Major, Minor int
	// Raw 工具 --version 的原始输出，用于详情文案
	Raw string
	// Err 找到了但无法确定版本时的原因
	Err error
}

// lookupTool 查找 name 并执行 --version 解析版本号
func lookupTool(ctx context.Context, name string) toolStatus {
	path, ok := ToolPath(name)
	if !ok {
		return toolStatus{Found: false}
	}
	major, minor, raw, err := ToolVersion(ctx, path)
	if err != nil {
		return toolStatus{Found: true, Err: err}
	}
	return toolStatus{Found: true, Major: major, Minor: minor, Raw: raw}
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
