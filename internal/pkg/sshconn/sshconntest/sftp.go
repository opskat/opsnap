package sshconntest

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
)

// sftpHandler 把 SFTP 的 "/" 映射到 root 目录，只实现浏览需要的 List 与 Stat。
//
// 用 RequestServer 而不是 sftp.NewServer：后者直接服务宿主机真实路径，
// 无法把测试根目录伪装成 "/"。
type sftpHandler struct{ root string }

func (h sftpHandler) local(p string) (string, error) {
	clean := path.Clean("/" + p)
	local := filepath.Join(h.root, filepath.FromSlash(clean))
	// 拦住越出 root 的路径
	if local != h.root && !strings.HasPrefix(local, h.root+string(filepath.Separator)) {
		return "", os.ErrPermission
	}
	return local, nil
}

func (h sftpHandler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	local, err := h.local(r.Filepath)
	if err != nil {
		return nil, err
	}
	switch r.Method {
	case "List":
		entries, err := os.ReadDir(local)
		if err != nil {
			return nil, err
		}
		infos := make([]os.FileInfo, 0, len(entries))
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			// os.DirEntry.Info 返回 lstat 语义，符号链接保持 ModeSymlink，
			// 与真实 OpenSSH sftp-server 的 readdir 一致。
			if e.Type()&fs.ModeSymlink != 0 {
				info = symlinkInfo{FileInfo: info}
			}
			infos = append(infos, info)
		}
		return listerAt(infos), nil
	case "Stat":
		info, err := os.Stat(local)
		if err != nil {
			return nil, err
		}
		return listerAt{info}, nil
	}
	return nil, errors.New("不支持的方法")
}

// symlinkInfo 让 readdir 返回的符号链接保持链接语义
type symlinkInfo struct{ os.FileInfo }

func (s symlinkInfo) Mode() os.FileMode { return s.FileInfo.Mode() | fs.ModeSymlink }

// listerAt 按偏移返回条目
type listerAt []os.FileInfo

func (l listerAt) ListAt(f []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(f, l[off:])
	if int(off)+n >= len(l) {
		return n, io.EOF
	}
	return n, nil
}
