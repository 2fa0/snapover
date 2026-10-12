package filetransfer

import "golang.org/x/sys/windows"

func publishFile(from, to string) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	// Without MOVEFILE_REPLACE_EXISTING, a concurrent destination is preserved.
	return windows.MoveFileEx(source, target, windows.MOVEFILE_WRITE_THROUGH)
}
