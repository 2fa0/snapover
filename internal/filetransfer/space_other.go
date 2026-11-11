//go:build !darwin && !windows

package filetransfer

func checkDiskSpace(dir string, size int64) error { return nil }
