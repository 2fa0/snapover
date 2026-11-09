package main

import (
	"testing"

	"github.com/egoist/mygo"
)

func TestCenterOnWorkArea(t *testing.T) {
	area := mygo.Rectangle{X: 1920, Y: 25, Width: 1440, Height: 850}
	got := centerOnWorkArea(area, 360, 132)
	want := mygo.Rectangle{X: 2460, Y: 384, Width: 360, Height: 132}
	if got != want {
		t.Fatalf("center = %+v, want %+v", got, want)
	}
	got = centerOnWorkArea(area, 2000, 2000)
	if got != area {
		t.Fatalf("oversized window = %+v, want the work area", got)
	}
}

func TestRelocateIfOffscreen(t *testing.T) {
	left := mygo.Display{WorkArea: mygo.Rectangle{X: 0, Y: 0, Width: 1512, Height: 982}, Primary: true}
	right := mygo.Display{WorkArea: mygo.Rectangle{X: 1512, Y: 0, Width: 2560, Height: 1440}}
	win := mygo.Rectangle{X: 1800, Y: 200, Width: 460, Height: 340}

	if got := relocateIfOffscreen(win, []mygo.Display{left, right}, left.WorkArea); got != win {
		t.Fatalf("still visible window moved: %+v", got)
	}

	got := relocateIfOffscreen(win, []mygo.Display{left}, left.WorkArea)
	want := centerOnWorkArea(left.WorkArea, win.Width, win.Height)
	if got != want {
		t.Fatalf("unplugged display = %+v, want %+v", got, want)
	}

	// If fallback itself points to the unplugged display, fall back to primary.
	if got := relocateIfOffscreen(win, []mygo.Display{left}, right.WorkArea); got != want {
		t.Fatalf("unplugged fallback display = %+v, want %+v", got, want)
	}

	if got := relocateIfOffscreen(win, nil, mygo.Rectangle{}); got != win {
		t.Fatalf("no displays moved the window: %+v", got)
	}
}

func TestHudOnWorkAreaPlacesInCorner(t *testing.T) {
	area := mygo.Rectangle{X: 0, Y: 25, Width: 1440, Height: 850}
	got := hudOnWorkArea(area, 360, 132)
	if got.X != 1440-360-16 || got.Width != 360 || got.Height != 132 {
		t.Fatalf("hud placement = %+v", got)
	}
	if got.Y < area.Y || got.Y+got.Height > area.Y+area.Height {
		t.Fatalf("hud outside work area vertically: %+v", got)
	}
}
