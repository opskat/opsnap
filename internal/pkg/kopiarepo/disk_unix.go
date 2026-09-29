//go:build unix

package kopiarepo

import "golang.org/x/sys/unix"

// DiskUsage 读取 path 所在文件系统的总量、已用与剩余
func DiskUsage(path string) (Disk, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Disk{}, err
	}
	bs := int64(st.Bsize) //nolint:unconvert // Bsize 在 Linux 为 int64、在 macOS 为 uint32
	return Disk{
		Total: int64(st.Blocks) * bs,          //nolint:gosec // 块数不会超过 int64
		Used:  int64(st.Blocks-st.Bfree) * bs, //nolint:gosec // 同上
		Free:  int64(st.Bavail) * bs,          //nolint:gosec // 同上
	}, nil
}
