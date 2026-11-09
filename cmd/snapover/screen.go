package main

import (
	"math"
	"runtime"

	"github.com/2fa0/snapover/internal/paste"
	"github.com/egoist/mygo"
)

func screenRectToDIP(r mygo.Rectangle, pixelOrigin mygo.Point, display mygo.Display) mygo.Rectangle {
	scale := display.ScaleFactor
	if scale <= 0 {
		scale = 1
	}
	toDIP := func(n int) int { return int(math.Floor(float64(n)/scale + 0.5)) }
	return mygo.Rectangle{
		X:     display.Bounds.X + toDIP(r.X-pixelOrigin.X),
		Y:     display.Bounds.Y + toDIP(r.Y-pixelOrigin.Y),
		Width: toDIP(r.Width), Height: toDIP(r.Height),
	}
}

func visibleOnDisplays(r mygo.Rectangle, displays []mygo.Display) bool {
	for _, d := range displays {
		a := d.WorkArea
		w := min(a.X+a.Width, r.X+r.Width) - max(a.X, r.X)
		h := min(a.Y+a.Height, r.Y+r.Height) - max(a.Y, r.Y)
		if w >= min(100, r.Width) && h >= min(50, r.Height) {
			return true
		}
	}
	return false
}

func centerOnWorkArea(area mygo.Rectangle, width, height int) mygo.Rectangle {
	if area.Width <= 0 || area.Height <= 0 {
		return mygo.Rectangle{Width: width, Height: height}
	}
	width = min(max(width, 1), area.Width)
	height = min(max(height, 1), area.Height)
	return mygo.Rectangle{
		X:      area.X + (area.Width-width)/2,
		Y:      area.Y + (area.Height-height)/2,
		Width:  width,
		Height: height,
	}
}

func hudOnWorkArea(area mygo.Rectangle, width, height int) mygo.Rectangle {
	if area.Width <= 0 || area.Height <= 0 {
		return mygo.Rectangle{Width: width, Height: height}
	}
	const margin = 16
	width = min(max(width, 1), area.Width)
	height = min(max(height, 1), area.Height)
	x := max(area.X, area.X+area.Width-width-margin)
	y := min(area.Y+margin, area.Y+area.Height-height)
	if runtime.GOOS == "windows" {
		y = max(area.Y, area.Y+area.Height-height-margin)
	}
	return mygo.Rectangle{X: x, Y: y, Width: width, Height: height}
}

func relocateIfOffscreen(r mygo.Rectangle, displays []mygo.Display, fallback mygo.Rectangle) mygo.Rectangle {
	if r.Width <= 0 || r.Height <= 0 || visibleOnDisplays(r, displays) {
		return r
	}
	area := fallback
	if area.Width <= 0 || area.Height <= 0 || (len(displays) > 0 && !visibleOnDisplays(area, displays)) {
		area = mygo.Rectangle{}
		for _, d := range displays {
			if d.Primary && d.WorkArea.Width > 0 {
				area = d.WorkArea
				break
			}
		}
	}
	if area.Width <= 0 || area.Height <= 0 {
		if len(displays) == 0 {
			return r
		}
		area = displays[0].WorkArea
	}
	if area.Width <= 0 || area.Height <= 0 {
		return r
	}
	return centerOnWorkArea(area, r.Width, r.Height)
}

func (a *app) workingWorkArea() mygo.Rectangle {
	if a.picker != nil && a.pickArea.Width > 0 && a.pickArea.Height > 0 {
		return a.pickArea
	}
	if live := paste.Capture(); live.Valid() {
		placed := pickerScreenBounds(live, 80, 80)
		if area := pickerAreaAt(placed); area.Width > 0 && area.Height > 0 {
			return area
		}
	}
	if a.win != nil {
		if area := windowWorkArea(a.win); area.Width > 0 && area.Height > 0 {
			return area
		}
	}
	return mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea
}

func (a *app) placeOnWorkingDisplay(opts mygo.WindowOptions) mygo.WindowOptions {
	area := a.workingWorkArea()
	if area.Width <= 0 || area.Height <= 0 || opts.Width <= 0 || opts.Height <= 0 {
		return opts
	}
	placed := centerOnWorkArea(area, opts.Width, opts.Height)
	opts.X, opts.Y = placed.X, placed.Y
	return opts
}

func (a *app) placeHUDOnWorkingDisplay(opts mygo.WindowOptions) mygo.WindowOptions {
	area := a.workingWorkArea()
	if area.Width <= 0 || area.Height <= 0 || opts.Width <= 0 || opts.Height <= 0 {
		return opts
	}
	placed := hudOnWorkArea(area, opts.Width, opts.Height)
	opts.X, opts.Y = placed.X, placed.Y
	return opts
}

func (a *app) clampWindowsToDisplays() {
	displays := mygo.Screen.Displays()
	fallback := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea
	move := func(win *mygo.Window) {
		if win == nil {
			return
		}
		b := win.Bounds()
		if b.Width <= 0 || b.Height <= 0 {
			return
		}
		next := relocateIfOffscreen(b, displays, fallback)
		if next != b {
			win.SetBounds(next)
		}
	}
	move(a.win)
	move(a.fileWin)
	move(a.transferWin)
	move(a.pickOriginal)
	if a.imageViewer != nil {
		move(a.imageViewer.win)
	}
	if a.networkGuide != nil {
		move(a.networkGuide.win)
	}
	if a.picker == nil || !a.pickPlaced {
		return
	}
	next := relocateIfOffscreen(a.pickRect, displays, fallback)
	if next == a.pickRect {
		return
	}
	a.pickRect = next
	a.pickSizeW, a.pickSizeH = next.Width, next.Height
	a.pickArea = pickerAreaAt(next)
	a.picker.SetBounds(next)
}

func windowWorkArea(win *mygo.Window) mygo.Rectangle {
	if win == nil {
		return mygo.Rectangle{}
	}
	b := win.Bounds()
	if b.Width <= 0 || b.Height <= 0 {
		return mygo.Rectangle{}
	}
	return mygo.Screen.DisplayNearestPoint(mygo.Point{X: b.X + b.Width/2, Y: b.Y + b.Height/2}).WorkArea
}
