package filetransfer

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func checkDiskSpace(dir string, size int64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return fmt.Errorf("无法检查接收磁盘：%w", err)
	}
	available := stat.Bavail
	if stat.Bsize > 0 {
		available *= uint64(stat.Bsize)
	}
	return checkSpace(size, available, unix.ByteSliceToString(stat.Fstypename[:]))
}
