//go:build darwin

package paste

import (
	"testing"
	"unsafe"
)

func TestWindowBoundsRejectsInvalidTarget(t *testing.T) {
	if _, ok := (Target{}).WindowBounds(); ok {
		t.Fatal("invalid target returned a window")
	}
}

func TestWindowBoundsReadsFocusedFrame(t *testing.T) {
	loadAX()
	trusted, create, copyAttr, timeout, release := axTrusted, axCreateApp, axCopyAttribute, axSetTimeout, axRelease
	getValue := axGetValue
	t.Cleanup(func() {
		axTrusted, axCreateApp, axCopyAttribute, axSetTimeout, axRelease = trusted, create, copyAttr, timeout, release
		axGetValue = getValue
	})
	axTrusted = func(uintptr) uint8 { return 1 }
	axCreateApp = func(int32) uintptr { return 1 }
	axRelease = func(uintptr) {}
	axSetTimeout = func(uintptr, float32) int32 { return 0 }
	axCopyAttribute = func(element, _ uintptr, value *uintptr) int32 {
		*value = element + 1
		return 0
	}
	axGetValue = func(_ uintptr, kind uint32, out unsafe.Pointer) uint8 {
		if kind == axValueCGRect {
			*(*struct{ X, Y, Width, Height float64 })(out) = struct{ X, Y, Width, Height float64 }{80, 40, 900, 700}
			return 1
		}
		return 0
	}
	got, ok := (Target{id: 123}).WindowBounds()
	if !ok || got != (Rectangle{X: 80, Y: 40, Width: 900, Height: 700}) {
		t.Fatalf("window = %+v, %v", got, ok)
	}
}
