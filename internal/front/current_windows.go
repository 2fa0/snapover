//go:build windows

package front

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Current is the foreground process name, without .exe.
// An empty string means the name could not be read.
func Current() string {
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || pid == 0 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 32767)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil || n == 0 {
		return ""
	}
	name := filepath.Base(windows.UTF16ToString(buf[:n]))
	return strings.TrimSuffix(strings.TrimSuffix(name, ".exe"), ".EXE")
}
