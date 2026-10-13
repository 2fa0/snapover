//go:build windows

package paste

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestCaretBoundsTracksTarget(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, ok := (Target{}).CaretBounds(); ok {
		t.Fatal("invalid target returned a caret")
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
	target := Target{id: hwnd}
	if _, ok := target.CaretBounds(); ok {
		t.Fatal("window without a caret returned bounds")
	}
	if ok, _, err := user32.NewProc("CreateCaret").Call(hwnd, 0, 2, 20); ok == 0 {
		t.Fatalf("CreateCaret: %v", err)
	}
	defer user32.NewProc("DestroyCaret").Call()
	for _, pos := range []struct{ X, Y int32 }{{30, 40}, {160, 90}} {
		if ok, _, err := user32.NewProc("SetCaretPos").Call(uintptr(pos.X), uintptr(pos.Y)); ok == 0 {
			t.Fatalf("SetCaretPos: %v", err)
		}
		want := pos
		if ok, _, err := procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&want))); ok == 0 {
			t.Fatalf("ClientToScreen: %v", err)
		}
		got, ok := target.CaretBounds()
		if !ok || got != (Rectangle{X: int(want.X), Y: int(want.Y), Width: 2, Height: 20}) {
			t.Fatalf("caret = %+v, available = %v, want screen point %+v with size 2x20", got, ok, want)
		}
	}
}
