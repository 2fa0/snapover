package main

import (
	"github.com/2fa0/snapover/internal/paste"
	"github.com/egoist/mygo"
)

func pickerScreenBounds(target paste.Target, width, height int) mygo.Rectangle {
	point := mygo.Screen.CursorScreenPoint()
	in := pickerPlaceInput{
		width: width, height: height,
		mouse:       mygo.Rectangle{X: point.X, Y: point.Y},
		mouseArea:   mygo.Screen.DisplayNearestPoint(point).WorkArea,
		mouseOnWork: true,
	}
	in.workArea = in.mouseArea
	if rect, ok := target.WindowBounds(); ok && rect.Width > 0 && rect.Height > 0 {
		in.window, in.hasWindow = pickerTargetRectangle(rect)
	}
	if in.hasWindow {
		center := mygo.Point{X: in.window.X + in.window.Width/2, Y: in.window.Y + in.window.Height/2}
		in.workArea = mygo.Screen.DisplayNearestPoint(center).WorkArea
		in.mouseOnWork = in.workArea == in.mouseArea
	}
	if !(in.hasWindow && in.mouseOnWork) {
		if rect, ok := target.CaretBounds(); ok {
			caret, ok := pickerTargetRectangle(rect)
			if !ok {
				return placeShortcutPicker(in)
			}
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
	return placeShortcutPicker(in)
}
