//go:build windows

package paste

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowBoundsTracksTarget(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, ok := (Target{}).WindowBounds(); ok {
		t.Fatal("invalid target returned a window")
	}
	class, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		t.Fatal(err)
	}
	hwnd, _, err := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0,
		0x00CF0000, 100, 100, 320, 240, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW: %v", err)
	}
	defer user32.NewProc("DestroyWindow").Call(hwnd)
	got, ok := (Target{id: hwnd}).WindowBounds()
	if !ok || got.Width < 320 || got.Height < 240 {
		t.Fatalf("window = %+v, available = %v", got, ok)
	}
}
