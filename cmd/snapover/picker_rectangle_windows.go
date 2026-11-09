//go:build windows

package main

import (
	"unsafe"

	"github.com/2fa0/snapover/internal/paste"
	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

var (
	pickerUser32          = windows.NewLazySystemDLL("user32.dll")
	pickerMonitorFromRect = pickerUser32.NewProc("MonitorFromRect")
	pickerMonitorInfo     = pickerUser32.NewProc("GetMonitorInfoW")
)

type pickerNativeRect struct{ Left, Top, Right, Bottom int32 }

// Accessibility returns physical pixels on Windows. MyGo's screen coordinates
// share display origins, rather than dividing absolute coordinates by DPI.
func pickerTargetRectangle(r paste.Rectangle) (mygo.Rectangle, bool) {
	native := pickerNativeRect{int32(r.X), int32(r.Y), int32(r.X + max(r.Width, 1)), int32(r.Y + max(r.Height, 1))}
	monitor, _, _ := pickerMonitorFromRect.Call(uintptr(unsafe.Pointer(&native)), 2)
	info := struct {
		Size          uint32
		Monitor, Work pickerNativeRect
		Flags         uint32
	}{}
	info.Size = uint32(unsafe.Sizeof(info))
	if got, _, _ := pickerMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); got == 0 {
		return mygo.Rectangle{}, false
	}
	for _, display := range mygo.Screen.Displays() {
		if display.ID == int64(monitor) {
			return screenRectToDIP(
				mygo.Rectangle{X: r.X, Y: r.Y, Width: r.Width, Height: r.Height},
				mygo.Point{X: int(info.Monitor.Left), Y: int(info.Monitor.Top)}, display), true
		}
	}
	return mygo.Rectangle{}, false
}
