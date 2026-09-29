package kopiarepo

// Disk 文件系统的用量（字节）
type Disk struct {
	Total int64
	Used  int64
	// Free 非特权进程可用的剩余空间，不含文件系统为 root 保留的部分
	Free int64
}
