//go:build integration

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// filesExclude 文件任务的排除规则；种子里放一个 .log 文件验证它确实没有进入快照
const filesExclude = "*.log"

// seedFiles 在与 target 共享的卷里写入虚构文件：多级目录、空格与中文文件名、可执行文件、空文件、
// 二进制大文件、符号链接、空目录，以及一个应被排除的日志文件
func seedFiles(t testing.TB) {
	t.Helper()
	root := seedFilesDir
	entries, err := os.ReadDir(root)
	check(t, err, "读取种子目录")
	for _, e := range entries {
		check(t, os.RemoveAll(filepath.Join(root, e.Name())), "清空种子目录")
	}
	rng := rand.New(rand.NewSource(42))
	blob := make([]byte, 3<<20)
	rng.Read(blob)
	write := func(rel string, data []byte, mode os.FileMode) {
		p := filepath.Join(root, rel)
		check(t, os.MkdirAll(filepath.Dir(p), 0o755), "创建目录")
		check(t, os.WriteFile(p, data, mode), "写入 %s", rel)
		check(t, os.Chmod(p, mode), "设置权限 %s", rel)
	}
	write("docs/readme.txt", []byte("这是 opsnap 集成测试的虚构文件。\nline 2\n"), 0o644)
	write("docs/空格 文件名.txt", []byte("名字里有空格和中文\n"), 0o644)
	write("bin/run.sh", []byte("#!/bin/sh\necho 虚构脚本\n"), 0o755)
	write("data/blob.bin", blob, 0o640)
	write("data/empty.txt", nil, 0o644)
	write("nested/a/b/c/deep.json", []byte(`{"depth":4,"name":"深层文件"}`), 0o600)
	write("logs/app.log", []byte("应被排除的日志\n"), 0o644)
	check(t, os.MkdirAll(filepath.Join(root, "emptydir"), 0o755), "创建空目录")
	check(t, os.Symlink("docs/readme.txt", filepath.Join(root, "link-to-readme")), "创建符号链接")
	// target 上的登录用户不是文件属主：目录统一 0755，文件加上其他人可读，否则 rsync 会报权限错误
	check(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return os.Chmod(p, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.Chmod(p, info.Mode().Perm()|0o004)
	}), "放开种子文件的读权限")
}

// fileEntry 一个条目的可比较描述：类型、权限位、内容摘要或链接目标
type fileEntry struct {
	Kind string
	Mode fs.FileMode
	Sum  string
}

func (e fileEntry) String() string { return fmt.Sprintf("%s %04o %s", e.Kind, e.Mode, e.Sum) }

// seedTree 种子目录里应进入快照的全部条目（去掉排除的日志文件）
func seedTree(t testing.TB) map[string]fileEntry {
	t.Helper()
	out := map[string]fileEntry{}
	check(t, filepath.WalkDir(seedFilesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(seedFilesDir, p)
		if rel == "." {
			return nil
		}
		if ok, _ := path.Match(filesExclude, d.Name()); ok {
			return nil
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out[rel] = fileEntry{Kind: "symlink", Sum: target}
		case info.IsDir():
			out[rel] = fileEntry{Kind: "dir", Mode: info.Mode().Perm()}
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[rel] = fileEntry{Kind: "file", Mode: info.Mode().Perm(), Sum: sha256Hex(data)}
		}
		return nil
	}), "遍历种子目录")
	return out
}

// tarTree 解开下载的 .tar.gz，取出种子目录（targetFilesDir 的最后一段）之下的条目
func tarTree(t testing.TB, tgz []byte) map[string]fileEntry {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	check(t, err, "下载内容不是 gzip")
	tr := tar.NewReader(zr)
	marker := path.Base(targetFilesDir) + "/"
	out := map[string]fileEntry{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		check(t, err, "读取 tar")
		name := strings.TrimSuffix(strings.TrimPrefix(h.Name, "./"), "/")
		i := strings.Index(name+"/", marker)
		if i < 0 {
			continue
		}
		rel := strings.TrimPrefix(name[i+len(marker)-1:], "/")
		if rel == "" {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			out[rel] = fileEntry{Kind: "dir", Mode: fs.FileMode(h.Mode).Perm()}
		case tar.TypeSymlink:
			out[rel] = fileEntry{Kind: "symlink", Sum: h.Linkname}
		case tar.TypeReg:
			data, err := io.ReadAll(tr)
			check(t, err, "读取 tar 条目 %s", h.Name)
			out[rel] = fileEntry{Kind: "file", Mode: fs.FileMode(h.Mode).Perm(), Sum: sha256Hex(data)}
		default:
			out[rel] = fileEntry{Kind: fmt.Sprintf("type-%c", h.Typeflag)}
		}
	}
	check(t, zr.Close(), "gzip 校验")
	return out
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// dirState 目录树的完整状态（路径、类型、大小、权限、修改时间、内容摘要），用于证明「什么都没有写入」
func dirState(t testing.TB, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	check(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		sum := ""
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum = sha256Hex(data)
		}
		out[p] = fmt.Sprintf("%v %d %d %s", info.Mode(), info.Size(), info.ModTime().UnixNano(), sum)
		return nil
	}), "遍历 %s", root)
	return out
}
