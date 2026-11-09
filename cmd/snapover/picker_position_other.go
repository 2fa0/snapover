//go:build !windows

package main

import (
	"github.com/2fa0/snapover/internal/paste"
	"github.com/egoist/mygo"
)

func pickerScreenBounds(target paste.Target, width, height int) (mygo.Rectangle, float64) {
	point := mygo.Screen.CursorScreenPoint()
	in := pickerPlaceInput{
		width: width, height: height,
		mouse:       mygo.Rectangle{X: point.X, Y: point.Y},
		mouseArea:   mygo.Screen.DisplayNearestPoint(point).WorkArea,
		mouseOnWork: true,
	}
	in.workArea = in.mouseArea
	if rect, ok := target.WindowBounds(); ok && rect.Width > 0 && rect.Height > 0 {
		in.window = mygo.Rectangle{X: rect.X, Y: rect.Y, Width: rect.Width, Height: rect.Height}
		in.hasWindow = true
		center := mygo.Point{X: in.window.X + in.window.Width/2, Y: in.window.Y + in.window.Height/2}
		in.workArea = mygo.Screen.DisplayNearestPoint(center).WorkArea
		in.mouseOnWork = in.workArea == in.mouseArea
	}
	if !(in.hasWindow && in.mouseOnWork) {
		if rect, ok := target.CaretBounds(); ok {
			caret := mygo.Rectangle{X: rect.X, Y: rect.Y, Width: rect.Width, Height: rect.Height}
			caretArea := mygo.Screen.DisplayNearestPoint(mygo.Point{X: caret.X, Y: caret.Y}).WorkArea
			if in.hasWindow {
				if caretArea == in.workArea && trustworthyPickerCaret(caret) {
					in.caret, in.hasCaret = caret, true
				}
			} else if caretArea != in.mouseArea && trustworthyPickerCaret(caret) {
				in.mouseOnWork = false
				in.caret, in.hasCaret = caret, true
				in.workArea = caretArea
			}
		}
	}
	return placeShortcutPicker(in), 1
}

func setPickerBounds(win *mygo.Window, bounds mygo.Rectangle, _ float64) {
	win.SetBounds(bounds)
}
