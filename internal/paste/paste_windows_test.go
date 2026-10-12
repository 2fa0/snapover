//go:build windows

package paste

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestFocusPreservesWindowState(t *testing.T) {
	createWindow := user32.NewProc("CreateWindowExW")
	destroyWindow := user32.NewProc("DestroyWindow")
	isZoomed := user32.NewProc("IsZoomed")
	class, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name      string
		show      uintptr
		minimized bool
		maximized bool
	}{
		{name: "normal", show: 1},
		{name: "maximized", show: 3, maximized: true},
		{name: "minimized normal", show: 1, minimized: true},
		{name: "minimized maximized", show: 3, minimized: true, maximized: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			const wsOverlappedWindow = 0x00CF0000
			hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(class)), 0,
				wsOverlappedWindow, 0, 0, 320, 240, 0, 0, 0, 0)
			if hwnd == 0 {
				t.Fatalf("CreateWindowExW: %v", err)
			}
			defer destroyWindow.Call(hwnd)
			procShow.Call(hwnd, tt.show)
			if tt.minimized {
				procShow.Call(hwnd, 6)
			}

			// Foreground activation can be denied; window state must still be preserved.
			focus(hwnd)
			if minimized, _, _ := procIsIconic.Call(hwnd); minimized != 0 {
				t.Fatal("target is still minimized")
			}
			maximized, _, _ := isZoomed.Call(hwnd)
			if (maximized != 0) != tt.maximized {
				t.Fatalf("maximized = %v, want %v", maximized != 0, tt.maximized)
			}
		})
	}
}
