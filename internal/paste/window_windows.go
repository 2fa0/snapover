//go:build windows

package paste

import "unsafe"

var procWindowRect = user32.NewProc("GetWindowRect")

// WindowBounds is the target's outer window in physical screen pixels.
func (t Target) WindowBounds() (Rectangle, bool) {
	if !t.Valid() {
		return Rectangle{}, false
	}
	var r caretRect
	if ok, _, _ := procWindowRect.Call(t.id, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return Rectangle{}, false
	}
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return Rectangle{}, false
	}
	return Rectangle{
		X: int(r.Left), Y: int(r.Top),
		Width: int(r.Right - r.Left), Height: int(r.Bottom - r.Top),
	}, true
}
