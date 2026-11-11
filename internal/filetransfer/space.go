package filetransfer

import (
	"fmt"
	"strings"
)

func checkSpace(size int64, available uint64, filesystem string) error {
	if (strings.EqualFold(filesystem, "msdos") || strings.EqualFold(filesystem, "FAT32") || strings.EqualFold(filesystem, "FAT")) && size > 1<<32-1 {
		return fmt.Errorf("接收文件系统 %s 不支持此大小的单文件，请改用 APFS、exFAT 或 NTFS 磁盘", filesystem)
	}
	if uint64(size) > available {
		return fmt.Errorf("接收磁盘空间不足：需要 %d 字节，可用 %d 字节", size, available)
	}
	return nil
}
