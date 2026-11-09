//go:build !windows

package main

import (
	"github.com/2fa0/snapover/internal/paste"
	"github.com/egoist/mygo"
)

func pickerTargetRectangle(r paste.Rectangle) (mygo.Rectangle, bool) {
	return mygo.Rectangle{X: r.X, Y: r.Y, Width: r.Width, Height: r.Height}, true
}
