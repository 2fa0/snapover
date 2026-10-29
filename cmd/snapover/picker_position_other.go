//go:build !windows

package main

import (
	"github.com/egoist/mygo"
	"github.com/2fa0/snapover/internal/paste"
)

func pickerScreenBounds(target paste.Target, width, height int) (mygo.Rectangle, float64) {
	point := mygo.Screen.CursorScreenPoint()
	anchor := mygo.Rectangle{X: point.X, Y: point.Y}
	if rect, ok := target.CaretBounds(); ok {
		anchor = mygo.Rectangle{X: rect.X, Y: rect.Y, Width: rect.Width, Height: rect.Height}
	}
	area := mygo.Screen.DisplayNearestPoint(mygo.Point{X: anchor.X, Y: anchor.Y}).WorkArea
	return pickerBounds(anchor, area, width, height), 1
}

func setPickerBounds(win *mygo.Window, bounds mygo.Rectangle, _ float64) {
	win.SetBounds(bounds)
}
