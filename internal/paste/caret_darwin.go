//go:build darwin

package paste

import (
	"math"
	"unsafe"
)

const (
	axValueCGRect  = 3
	axValueCFRange = 4
)

// CaretBounds reads the focused text element before the picker takes focus.
// Call it on the main thread. Apps without text accessibility return false.
func (t Target) CaretBounds() (Rectangle, bool) {
	if !t.Valid() || !Allowed() {
		return Rectangle{}, false
	}
	app := axCreateApp(int32(t.id))
	if app == 0 {
		return Rectangle{}, false
	}
	defer axRelease(app)
	// An unresponsive target must not hold up the shortcut.
	axSetTimeout(app, 0.2)
	var element uintptr
	if axCopyAttribute(app, uintptr(nsString("AXFocusedUIElement")), &element) != 0 || element == 0 {
		return Rectangle{}, false
	}
	defer axRelease(element)
	var selected uintptr
	if axCopyAttribute(element, uintptr(nsString("AXSelectedTextRange")), &selected) != 0 || selected == 0 {
		return Rectangle{}, false
	}
	defer axRelease(selected)
	var span struct{ Location, Length int64 }
	if axGetValue(selected, axValueCFRange, unsafe.Pointer(&span)) == 0 {
		return Rectangle{}, false
	}
	span.Length = 0
	rangeValue := axCreateValue(axValueCFRange, unsafe.Pointer(&span))
	if rangeValue == 0 {
		return Rectangle{}, false
	}
	defer axRelease(rangeValue)
	var bounds uintptr
	if axCopyParameterized(element, uintptr(nsString("AXBoundsForRange")), rangeValue, &bounds) != 0 || bounds == 0 {
		return Rectangle{}, false
	}
	defer axRelease(bounds)
	var rect struct{ X, Y, Width, Height float64 }
	if axGetValue(bounds, axValueCGRect, unsafe.Pointer(&rect)) == 0 ||
		math.IsNaN(rect.X) || math.IsInf(rect.X, 0) || math.IsNaN(rect.Y) || math.IsInf(rect.Y, 0) ||
		math.IsNaN(rect.Width) || math.IsInf(rect.Width, 0) || math.IsNaN(rect.Height) || math.IsInf(rect.Height, 0) || rect.Height <= 0 {
		return Rectangle{}, false
	}
	return Rectangle{X: int(math.Floor(rect.X)), Y: int(math.Floor(rect.Y)), Width: int(math.Ceil(rect.Width)), Height: int(math.Ceil(rect.Height))}, true
}
