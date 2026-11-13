//go:build windows

package front

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// CurrentSource is the foreground process, named without .exe and identified
// by its executable path, which does not change with the window title.
func CurrentSource() Source {
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		return Source{}
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || pid == 0 {
		return Source{}
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return Source{}
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 32767)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil || n == 0 {
		return Source{}
	}
	path := windows.UTF16ToString(buf[:n])
	name := filepath.Base(path)
	return Source{Name: strings.TrimSuffix(strings.TrimSuffix(name, ".exe"), ".EXE"), ID: path}
}
