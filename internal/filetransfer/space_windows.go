package filetransfer

import (
	"fmt"
	"golang.org/x/sys/windows"
)

func checkDiskSpace(dir string, size int64) error {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	var free, total, unused uint64
	if err := windows.GetDiskFreeSpaceEx(path, &free, &total, &unused); err != nil {
		return fmt.Errorf("无法检查接收磁盘：%w", err)
	}
	var root [32768]uint16
	if err := windows.GetVolumePathName(path, &root[0], uint32(len(root))); err != nil {
		return fmt.Errorf("无法检查接收文件系统：%w", err)
	}
	var fs [256]uint16
	if err := windows.GetVolumeInformation(&root[0], nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))); err != nil {
		return fmt.Errorf("无法检查接收文件系统：%w", err)
	}
	return checkSpace(size, free, windows.UTF16ToString(fs[:]))
}
