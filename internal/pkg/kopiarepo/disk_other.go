//go:build !unix

package kopiarepo

import "errors"

// DiskUsage 只支持类 Unix 系统（部署目标）
func DiskUsage(string) (Disk, error) {
	return Disk{}, errors.ErrUnsupported
}
