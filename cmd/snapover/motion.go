package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

func press(e ui.Element) ui.Element {
	p := ui.Pose{}
	if e.Pressed() {
		p.Scale = 0.97
	}
	return e.AnimatePose(p, ui.Snappy)
}

func arrive(e ui.Element) ui.Element {
	p := e.Keyframes("arrive", []ui.Keyframe{
		{Value: 1},
		{At: 180 * time.Millisecond, Value: 0, Ease: ui.EaseOut},
	})
	return e.Translate(0, 4*p).Opacity(1 - p)
}
