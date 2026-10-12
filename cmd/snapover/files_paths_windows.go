package main

import (
	"golang.org/x/sys/windows"
	"path/filepath"
)

func defaultReceiveDir() string {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Snapover")
}
