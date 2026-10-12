//go:build darwin

package paste

import (
	"testing"
	"unsafe"
)

func TestCaretAXValueBindings(t *testing.T) {
	loadAX()
	if axErr != nil {
		t.Fatal(axErr)
	}
	span := struct{ Location, Length int64 }{Location: 12}
	rangeValue := axCreateValue(axValueCFRange, unsafe.Pointer(&span))
	if rangeValue == 0 {
		t.Fatal("could not create accessibility text range")
	}
	defer axRelease(rangeValue)
	var gotSpan struct{ Location, Length int64 }
	if axGetValue(rangeValue, axValueCFRange, unsafe.Pointer(&gotSpan)) == 0 || gotSpan != span {
		t.Fatalf("text range = %+v, want %+v", gotSpan, span)
	}
	rect := struct{ X, Y, Width, Height float64 }{X: -1500, Y: 200, Width: 0, Height: 20}
	value := axCreateValue(axValueCGRect, unsafe.Pointer(&rect))
	if value == 0 {
		t.Fatal("could not create accessibility caret rectangle")
	}
	defer axRelease(value)
	var gotRect struct{ X, Y, Width, Height float64 }
	if axGetValue(value, axValueCGRect, unsafe.Pointer(&gotRect)) == 0 || gotRect != rect {
		t.Fatalf("caret rectangle = %+v, want %+v", gotRect, rect)
	}
	if _, ok := (Target{}).CaretBounds(); ok {
		t.Fatal("invalid target returned a caret")
	}
}
