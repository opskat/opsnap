package repo_svc

import "syscall"

// freeSpace 目录所在文件系统对普通用户可用的剩余空间（字节）
func freeSpace(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
