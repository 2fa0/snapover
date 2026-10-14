//go:build darwin

package paste

import (
	"testing"
	"time"
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

func mockCaretAX(t *testing.T) {
	t.Helper()
	loadAX()
	trusted, create, copyAttr, timeout, release := axTrusted, axCreateApp, axCopyAttribute, axSetTimeout, axRelease
	getValue, createValue, copyParam := axGetValue, axCreateValue, axCopyParameterized
	t.Cleanup(func() {
		axTrusted, axCreateApp, axCopyAttribute, axSetTimeout, axRelease = trusted, create, copyAttr, timeout, release
		axGetValue, axCreateValue, axCopyParameterized = getValue, createValue, copyParam
	})
	axTrusted = func(uintptr) uint8 { return 1 }
	axCreateApp = func(int32) uintptr { return 1 }
	axRelease = func(uintptr) {}
	axCopyAttribute = func(element, _ uintptr, value *uintptr) int32 {
		*value = element + 1
		return 0
	}
	axSetTimeout = func(uintptr, float32) int32 { return 0 }
	axGetValue = func(_ uintptr, kind uint32, out unsafe.Pointer) uint8 {
		if kind == axValueCGRect {
			*(*struct{ X, Y, Width, Height float64 })(out) = struct{ X, Y, Width, Height float64 }{100, 200, 0, 20}
		}
		return 1
	}
	axCreateValue = func(uint32, unsafe.Pointer) uintptr { return 4 }
	axCopyParameterized = func(_, _, _ uintptr, value *uintptr) int32 {
		*value = 5
		return 0
	}
}

func TestCaretQueriesShareTimeout(t *testing.T) {
	mockCaretAX(t)
	var elements []uintptr
	var budgets []float32
	axSetTimeout = func(element uintptr, seconds float32) int32 {
		elements = append(elements, element)
		budgets = append(budgets, seconds)
		return 0
	}
	copyAttr := axCopyAttribute
	axCopyAttribute = func(element, attribute uintptr, value *uintptr) int32 {
		if len(elements) == 0 || elements[len(elements)-1] != element {
			t.Fatal("queried an AX object without applying its timeout")
		}
		time.Sleep(time.Millisecond)
		return copyAttr(element, attribute, value)
	}
	rect, ok := (Target{id: 123}).CaretBounds()
	if !ok || rect != (Rectangle{X: 100, Y: 200, Height: 20}) {
		t.Fatalf("caret = %+v, %v", rect, ok)
	}
	if len(elements) != 3 || elements[0] != 1 || elements[1] != 2 || elements[2] != 2 {
		t.Fatalf("timeout objects = %v", elements)
	}
	if budgets[0] > float32(caretTimeout.Seconds()) || budgets[1] >= budgets[0] || budgets[2] >= budgets[1] {
		t.Fatalf("timeouts did not share a decreasing budget: %v", budgets)
	}
}

func TestCaretStopsWhenTimeoutBudgetExpires(t *testing.T) {
	mockCaretAX(t)
	queries := 0
	axCopyAttribute = func(_ uintptr, _ uintptr, value *uintptr) int32 {
		queries++
		time.Sleep(caretTimeout + time.Millisecond)
		*value = 2
		return 0
	}
	if _, ok := (Target{id: 123}).CaretBounds(); ok || queries != 1 {
		t.Fatalf("continued after exhausted budget: ok=%v queries=%d", ok, queries)
	}
}
