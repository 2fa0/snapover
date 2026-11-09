//go:build windows

package clip

import (
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ownerUser32                  = windows.NewLazySystemDLL("user32.dll")
	procGetClipboardOwner        = ownerUser32.NewProc("GetClipboardOwner")
	procGetWindowThreadProcessId = ownerUser32.NewProc("GetWindowThreadProcessId")
)

// OwnerProcess returns the executable base name that currently owns the
// clipboard, such as rdpclip.exe. It does not open the clipboard and must not
// be used for loop detection or rewrite policy.
func OwnerProcess() string {
	hwnd, _, _ := procGetClipboardOwner.Call()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var size uint32 = 260
	buf := make([]uint16, size)
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:size]))
}
