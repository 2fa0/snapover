//go:build windows

package main

import (
	"math"
	"unsafe"

	"github.com/egoist/mygo"
	"github.com/2fa0/snapover/internal/paste"
	"golang.org/x/sys/windows"
)

var (
	pickerUser32          = windows.NewLazySystemDLL("user32.dll")
	pickerCursorPos       = pickerUser32.NewProc("GetCursorPos")
	pickerMonitorFromRect = pickerUser32.NewProc("MonitorFromRect")
	pickerMonitorInfo     = pickerUser32.NewProc("GetMonitorInfoW")
	pickerSetWindowPos    = pickerUser32.NewProc("SetWindowPos")
	pickerMonitorDPI      = windows.NewLazySystemDLL("shcore.dll").NewProc("GetDpiForMonitor")
)

type pickerNativeRect struct{ Left, Top, Right, Bottom int32 }

func pickerScreenBounds(target paste.Target, width, height int) (mygo.Rectangle, float64) {
	var point struct{ X, Y int32 }
	pickerCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	anchor := mygo.Rectangle{X: int(point.X), Y: int(point.Y)}
	if rect, ok := target.CaretBounds(); ok {
		anchor = mygo.Rectangle{X: rect.X, Y: rect.Y, Width: rect.Width, Height: rect.Height}
	}
	// Select the monitor in physical coordinates; monitors with different
	// scales can overlap after each one's coordinates are converted to DIPs.
	r := pickerNativeRect{int32(anchor.X), int32(anchor.Y), int32(anchor.X + max(anchor.Width, 1)), int32(anchor.Y + max(anchor.Height, 1))}
	monitor, _, _ := pickerMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)), 2)
	info := struct {
		Size          uint32
		Monitor, Work pickerNativeRect
		Flags         uint32
	}{}
	info.Size = uint32(unsafe.Sizeof(info))
	if ok, _, _ := pickerMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
		// Screen APIs normally succeed; retain mouse positioning on failure.
		point := mygo.Screen.CursorScreenPoint()
		display := mygo.Screen.DisplayNearestPoint(point)
		return pickerBounds(mygo.Rectangle{X: point.X, Y: point.Y}, display.WorkArea, width, height), display.ScaleFactor
	}
	xDPI, yDPI := uint32(96), uint32(96)
	if hr, _, _ := pickerMonitorDPI.Call(monitor, 0, uintptr(unsafe.Pointer(&xDPI)), uintptr(unsafe.Pointer(&yDPI))); hr != 0 {
		xDPI = 96
	}
	scale := float64(xDPI) / 96
	toDIP := func(n int) int { return int(math.Round(float64(n) / scale)) }
	work := info.Work
	area := mygo.Rectangle{X: toDIP(int(work.Left)), Y: toDIP(int(work.Top)), Width: toDIP(int(work.Right - work.Left)), Height: toDIP(int(work.Bottom - work.Top))}
	anchor = mygo.Rectangle{X: toDIP(anchor.X), Y: toDIP(anchor.Y), Width: toDIP(anchor.Width), Height: toDIP(anchor.Height)}
	return pickerBounds(anchor, area, width, height), scale
}

func setPickerBounds(win *mygo.Window, bounds mygo.Rectangle, scale float64) {
	toPx := func(n int) uintptr { return uintptr(int(math.Round(float64(n) * scale))) }
	// SetBounds uses the window's current DPI, initially the tray monitor's.
	// Use physical pixels so moving to a differently scaled monitor is correct.
	const noZOrderNoActivate = 0x0004 | 0x0010
	// Move first so WM_DPICHANGED's suggested rectangle cannot override the
	// final size or push the picker outside the destination work area.
	pickerSetWindowPos.Call(win.NativeHandle(), 0, toPx(bounds.X), toPx(bounds.Y), 0, 0, noZOrderNoActivate|0x0001)
	pickerSetWindowPos.Call(win.NativeHandle(), 0, toPx(bounds.X), toPx(bounds.Y), toPx(bounds.Width), toPx(bounds.Height), noZOrderNoActivate)
}
