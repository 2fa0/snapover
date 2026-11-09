//go:build darwin

package paste

import (
	"math"
	"time"
	"unsafe"
)

// WindowBounds reads the focused window before the picker takes focus.
// Call it on the main thread. Apps without window accessibility return false.
func (t Target) WindowBounds() (Rectangle, bool) {
	if !t.Valid() || !Allowed() {
		return Rectangle{}, false
	}
	app := axCreateApp(int32(t.id))
	if app == 0 {
		return Rectangle{}, false
	}
	defer axRelease(app)
	deadline := time.Now().Add(caretTimeout)
	setTimeout := func(element uintptr) bool {
		left := time.Until(deadline)
		return left > 0 && axSetTimeout(element, float32(left.Seconds())) == 0
	}
	if !setTimeout(app) {
		return Rectangle{}, false
	}
	var win uintptr
	if axCopyAttribute(app, uintptr(nsString("AXFocusedWindow")), &win) != 0 || win == 0 {
		if axCopyAttribute(app, uintptr(nsString("AXMainWindow")), &win) != 0 || win == 0 {
			return Rectangle{}, false
		}
	}
	defer axRelease(win)
	if !setTimeout(win) {
		return Rectangle{}, false
	}
	return axWindowFrame(win)
}

func axWindowFrame(win uintptr) (Rectangle, bool) {
	var frame uintptr
	if axCopyAttribute(win, uintptr(nsString("AXFrame")), &frame) == 0 && frame != 0 {
		defer axRelease(frame)
		var rect struct{ X, Y, Width, Height float64 }
		if axGetValue(frame, axValueCGRect, unsafe.Pointer(&rect)) != 0 {
			return axFiniteRect(rect)
		}
	}
	var posRef, sizeRef uintptr
	if axCopyAttribute(win, uintptr(nsString("AXPosition")), &posRef) != 0 || posRef == 0 {
		return Rectangle{}, false
	}
	defer axRelease(posRef)
	if axCopyAttribute(win, uintptr(nsString("AXSize")), &sizeRef) != 0 || sizeRef == 0 {
		return Rectangle{}, false
	}
	defer axRelease(sizeRef)
	var pos struct{ X, Y float64 }
	var size struct{ Width, Height float64 }
	if axGetValue(posRef, axValueCGPoint, unsafe.Pointer(&pos)) == 0 ||
		axGetValue(sizeRef, axValueCGSize, unsafe.Pointer(&size)) == 0 {
		return Rectangle{}, false
	}
	return axFiniteRect(struct{ X, Y, Width, Height float64 }{pos.X, pos.Y, size.Width, size.Height})
}

func axFiniteRect(rect struct{ X, Y, Width, Height float64 }) (Rectangle, bool) {
	if math.IsNaN(rect.X) || math.IsInf(rect.X, 0) || math.IsNaN(rect.Y) || math.IsInf(rect.Y, 0) ||
		math.IsNaN(rect.Width) || math.IsInf(rect.Width, 0) || math.IsNaN(rect.Height) || math.IsInf(rect.Height, 0) ||
		rect.Width <= 0 || rect.Height <= 0 {
		return Rectangle{}, false
	}
	return Rectangle{
		X: int(math.Floor(rect.X)), Y: int(math.Floor(rect.Y)),
		Width: int(math.Ceil(rect.Width)), Height: int(math.Ceil(rect.Height)),
	}, true
}
