//go:build windows

package apps

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var getWindowTextLength = windows.NewLazySystemDLL("user32.dll").NewProc("GetWindowTextLengthW")

// running lists the processes that own a visible window with a title. A
// Windows process has no icon of its own here: the list shows names.
func running() []App {
	self, _ := os.Executable()
	seen := make(map[string]bool)
	var list []App
	callback := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if !windows.IsWindowVisible(windows.HWND(hwnd)) {
			return 1
		}
		if length, _, _ := getWindowTextLength.Call(hwnd); length == 0 {
			return 1
		}
		entry, ok := sourceAt(hwnd)
		if !ok || strings.EqualFold(entry.ID, self) || seen[entry.ID] {
			return 1
		}
		seen[entry.ID] = true
		list = append(list, entry)
		return 1
	})
	if err := windows.EnumWindows(callback, nil); err != nil {
		return nil
	}
	return list
}

func describe(path string) (App, error) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if name == "" {
		return App{}, errors.New("不是应用程序")
	}
	return App{Name: name, ID: path}, nil
}

// sourceAt names the process behind a window. internal/front reads the same
// fields for the window in front.
func sourceAt(hwnd uintptr) (App, bool) {
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid); err != nil || pid == 0 {
		return App{}, false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return App{}, false
	}
	defer windows.CloseHandle(handle)
	buf := make([]uint16, 32767)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil || size == 0 {
		return App{}, false
	}
	path := windows.UTF16ToString(buf[:size])
	name := filepath.Base(path)
	return App{Name: strings.TrimSuffix(strings.TrimSuffix(name, ".exe"), ".EXE"), ID: path}, true
}
