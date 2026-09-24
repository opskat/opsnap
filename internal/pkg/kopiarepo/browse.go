package kopiarepo

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// DirStatus 目录浏览中每个子目录的状态
type DirStatus string

const (
	DirEmpty       DirStatus = "empty"
	DirRepository  DirStatus = "repository"
	DirNotEmpty    DirStatus = "not_empty"
	DirNotWritable DirStatus = "not_writable" // OpsNap 进程对它没有写权限
	DirNoAccess    DirStatus = "no_access"    // 无权读取，不能进入
)

// Dir 一个子目录
type Dir struct {
	Name   string
	Path   string
	Status DirStatus
}

// ErrInvalidDirName 新建文件夹的名称为空、含 / 或为 . / ..
var ErrInvalidDirName = errors.New("文件夹名称不能为空，也不能包含 /")

// ListDirs 列出 path 下的子目录（含指向目录的符号链接），不列文件，按名称排序。
func ListDirs(path string) ([]Dir, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrRelativePath
	}
	path = filepath.Clean(path)
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	dirs := []Dir{}
	for _, e := range entries {
		full := filepath.Join(path, e.Name())
		if !e.IsDir() {
			if e.Type()&os.ModeSymlink == 0 {
				continue
			}
			fi, err := os.Stat(full)
			if err != nil || !fi.IsDir() {
				continue
			}
		}
		dirs = append(dirs, Dir{Name: e.Name(), Path: full, Status: dirStatus(full)})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	return dirs, nil
}

func dirStatus(path string) DirStatus {
	children, err := os.ReadDir(path)
	if err != nil {
		return DirNoAccess
	}
	if unix.Access(path, unix.W_OK) != nil {
		return DirNotWritable
	}
	if len(children) == 0 {
		return DirEmpty
	}
	if _, ok := localRepository(path); ok {
		return DirRepository
	}
	return DirNotEmpty
}

// Mkdir 在 parent 下以 0700 新建文件夹并返回其路径；重名返回 os.ErrExist，无写权限返回 os.ErrPermission。
func Mkdir(parent, name string) (string, error) {
	if !filepath.IsAbs(parent) {
		return "", ErrRelativePath
	}
	if strings.TrimSpace(name) == "" || strings.Contains(name, "/") || name == "." || name == ".." {
		return "", ErrInvalidDirName
	}
	p := filepath.Join(filepath.Clean(parent), name)
	if err := os.Mkdir(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}
