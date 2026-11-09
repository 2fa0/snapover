//go:build windows

package main

import (
	"math"
	"unsafe"

	"github.com/2fa0/snapover/internal/paste"
	"github.com/egoist/mygo"
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

func pickerMonitorAt(x, y int) (monitor uintptr, area mygo.Rectangle, scale float64, ok bool) {
	r := pickerNativeRect{int32(x), int32(y), int32(x + 1), int32(y + 1)}
	monitor, _, _ = pickerMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)), 2)
	info := struct {
		Size          uint32
		Monitor, Work pickerNativeRect
		Flags         uint32
	}{}
	info.Size = uint32(unsafe.Sizeof(info))
	if got, _, _ := pickerMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); got == 0 {
		return 0, mygo.Rectangle{}, 1, false
	}
	xDPI, yDPI := uint32(96), uint32(96)
	if hr, _, _ := pickerMonitorDPI.Call(monitor, 0, uintptr(unsafe.Pointer(&xDPI)), uintptr(unsafe.Pointer(&yDPI))); hr != 0 {
		xDPI = 96
	}
	scale = float64(xDPI) / 96
	if scale <= 0 {
		scale = 1
	}
	toDIP := func(n int) int { return int(math.Round(float64(n) / scale)) }
	work := info.Work
	area = mygo.Rectangle{X: toDIP(int(work.Left)), Y: toDIP(int(work.Top)), Width: toDIP(int(work.Right - work.Left)), Height: toDIP(int(work.Bottom - work.Top))}
	return monitor, area, scale, true
}

func pickerToDIP(r mygo.Rectangle, scale float64) mygo.Rectangle {
	toDIP := func(n int) int { return int(math.Round(float64(n) / scale)) }
	return mygo.Rectangle{X: toDIP(r.X), Y: toDIP(r.Y), Width: toDIP(r.Width), Height: toDIP(r.Height)}
}

func pickerScreenBounds(target paste.Target, width, height int) (mygo.Rectangle, float64) {
	var point struct{ X, Y int32 }
	pickerCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	mousePhys := mygo.Rectangle{X: int(point.X), Y: int(point.Y)}
	mouseMon, mouseArea, mouseScale, mouseOK := pickerMonitorAt(mousePhys.X, mousePhys.Y)
	if !mouseOK {
		point := mygo.Screen.CursorScreenPoint()
		display := mygo.Screen.DisplayNearestPoint(point)
		return pickerBounds(mygo.Rectangle{X: point.X, Y: point.Y}, display.WorkArea, width, height), display.ScaleFactor
	}

	workMon, workArea, workScale := mouseMon, mouseArea, mouseScale
	mouseOnWork := true
	var winPhys mygo.Rectangle
	hasWindow := false
	if rect, ok := target.WindowBounds(); ok && rect.Width > 0 && rect.Height > 0 {
		winPhys = mygo.Rectangle{X: rect.X, Y: rect.Y, Width: rect.Width, Height: rect.Height}
		if mon, area, scale, ok := pickerMonitorAt(winPhys.X+winPhys.Width/2, winPhys.Y+winPhys.Height/2); ok {
			hasWindow = true
			workMon, workArea, workScale = mon, area, scale
			mouseOnWork = workMon == mouseMon
		}
	}

	var caret mygo.Rectangle
	hasCaret := false
	if !(hasWindow && mouseOnWork) {
		if rect, ok := target.CaretBounds(); ok {
			caretPhys := mygo.Rectangle{X: rect.X, Y: rect.Y, Width: rect.Width, Height: rect.Height}
			if caretMon, caretArea, caretScale, ok := pickerMonitorAt(caretPhys.X, caretPhys.Y); ok {
				caretDIP := pickerToDIP(caretPhys, caretScale)
				if hasWindow {
					if caretMon == workMon && trustworthyPickerCaret(caretDIP) {
						caret, hasCaret = caretDIP, true
					}
				} else if caretMon != mouseMon && trustworthyPickerCaret(caretDIP) {
					mouseOnWork = false
					caret, hasCaret = caretDIP, true
					workMon, workArea, workScale = caretMon, caretArea, caretScale
				}
			}
		}
	}

	scale := mouseScale
	if !mouseOnWork {
		scale = workScale
	}
	in := pickerPlaceInput{
		width: width, height: height,
		mouse:       pickerToDIP(mousePhys, mouseScale),
		mouseArea:   mouseArea,
		mouseOnWork: mouseOnWork,
		workArea:    workArea,
	}
	if hasCaret {
		in.caret, in.hasCaret = caret, true
	}
	if hasWindow {
		in.window = pickerToDIP(winPhys, workScale)
		in.hasWindow = true
	}
	return placeShortcutPicker(in), scale
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
