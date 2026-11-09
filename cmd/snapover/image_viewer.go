package main

// Adapted from Lobe MyGo's ImageViewer (MIT, Copyright 2026 LobeHub). See
// licenses/lobe-mygo.txt for the source and the license. The window, the
// decode and the memory bound are Snapover's: one viewer at a time, one
// bounded bitmap, released when the window closes.

import (
	"context"
	"fmt"
	"image"
	"math"

	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"
)

const (
	// viewerMaxPixels bounds what the viewer keeps decoded. A 5K screenshot
	// decodes whole, over it the picture is scaled down: the window shows a
	// small share of it anyway, and four bytes a pixel of an original is
	// what a utility of this size cannot hold while it sits open.
	viewerMaxPixels = 10_000_000
	// viewerMargin is the room the picture keeps from the window's edges when
	// it is fitted.
	viewerMargin = 24
	// viewerMaxZoom is how far it enlarges beyond fitting, and viewerStep what
	// a click of the buttons or a turn of the wheel does.
	viewerMaxZoom = 8
	viewerStep    = 1.5
	// viewerWheelZoom is how fast a pinch zooms, and viewerWheelStep what one
	// notch of a mouse wheel does: a trackpad pans, and its pinches zoom.
	viewerWheelZoom = 0.0025
	viewerWheelStep = 1.15
)

type imageViewerState struct {
	app *app
	id  string
	win *mygo.Window

	bmp     *ui.Bitmap
	width   int
	height  int
	err     string
	note    string
	opening bool

	// scale is how much larger than fitted the picture shows; panX and panY
	// move it from the middle of the window.
	scale      float32
	panX, panY float32
	dpi        float32
	dpiKnown   bool
	dragging   bool
	dragX      float32
	dragY      float32
	dragPanX   float32
	dragPanY   float32

	// viewW and viewH are the room the picture has, as of the last frame.
	viewW, viewH float32
	// request names the decode that is running, so that a closed window never
	// paints a late one; openRequest does the same for handing the original to
	// the system viewer.
	request     *struct{ id string }
	openRequest *struct{}
	closed      bool
}

// openImageViewer shows an image at the size the user asks for, in a window
// of its own, and hands the system viewer the original on request.
func (a *app) openImageViewer(entry history.Entry) {
	// Whatever hover preview led here has served its purpose: the window
	// shows the picture now.
	a.closePickPreview()
	a.closeImageViewer()
	v := &imageViewerState{app: a, id: entry.ID, scale: 1, dpi: 1}
	var win *mygo.Window
	win = mygo.NewWindow(a.placeOnWorkingDisplay(mygo.WindowOptions{
		Title:    a.tr("图片预览"),
		Width:    900,
		Height:   680,
		MinWidth: 360, MinHeight: 240,
		Hidden: true, UseContentSize: true,
		Content: ui.View(func(c *ui.Context) { v.view(c) }),
	}))
	// An empty menu overrides the application menu; nil inherits it on Windows.
	win.SetMenu(mygo.NewMenu(nil))
	v.win = win
	a.imageViewer = v
	win.OnClosed(func() {
		v.release()
		if a.imageViewer == v {
			a.imageViewer = nil
		}
	})
	win.Show()
	win.Focus()
	a.loadViewerImage(v)
}

func (a *app) closeImageViewer() {
	v := a.imageViewer
	if v == nil {
		return
	}
	a.imageViewer = nil
	v.release()
	if v.win != nil {
		v.win.Close()
	}
}

// release drops the pixels and turns away anything still on its way: a closed
// viewer holds no image and launches nothing.
func (v *imageViewerState) release() {
	v.bmp = nil
	v.closed, v.openRequest = true, nil
}

func (a *app) loadViewerImage(v *imageViewerState) {
	if v.bmp != nil || v.request != nil {
		return
	}
	request := &struct{ id string }{v.id}
	v.request = request
	store := a.hist
	go func() {
		img, err := store.Image(v.id)
		var bmp *ui.Bitmap
		message := ""
		if err == nil {
			img = boundedViewerImage(img)
			bmp = ui.NewBitmap(imageForDisplay(img))
		} else {
			message = "图片读取失败，请重新复制后再试"
		}
		mygo.RunOnMain(func() {
			if v.request != request {
				return
			}
			v.request = nil
			v.bmp, v.err = bmp, message
			if bmp != nil {
				v.width, v.height = bmp.Size()
			}
			if v.win != nil {
				v.win.Invalidate()
			}
		})
	}()
}

// boundedViewerImage scales a picture down to viewerMaxPixels. The decode of
// the original is what the clipboard already allows itself; what it keeps is
// this bound.
func boundedViewerImage(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 || w*h <= viewerMaxPixels {
		return img
	}
	by := math.Sqrt(float64(w) * float64(h) / viewerMaxPixels)
	tw, th := max(1, int(float64(w)/by)), max(1, int(float64(h)/by))
	dst := image.NewNRGBA(image.Rect(0, 0, tw, th))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// natural is the picture's size in DIPs at one image pixel to one device
// pixel, which is what "actual size" shows.
func (v *imageViewerState) natural() (w, h float32) {
	if v.width <= 0 || v.height <= 0 {
		return 0, 0
	}
	dpi := v.dpi
	if dpi <= 0 {
		dpi = 1
	}
	return float32(v.width) / dpi, float32(v.height) / dpi
}

// fitScale is how much the picture shrinks to fit the window, and 1 for one
// that fits as it is: a small picture is not blown up to fill the window.
func (v *imageViewerState) fitScale() float32 {
	nw, nh := v.natural()
	if nw <= 0 || nh <= 0 || v.viewW <= 0 || v.viewH <= 0 {
		return 1
	}
	return min(max((v.viewW-2*viewerMargin)/nw, 0.01), (v.viewH-2*viewerMargin)/nh, 1)
}

// shown is the picture's size in the window, and the share of its pixels it
// shows, which "actual size" brings to 1.
func (v *imageViewerState) shown() (w, h, share float32) {
	nw, nh := v.natural()
	fit := v.fitScale()
	w, h = nw*fit*v.scale, nh*fit*v.scale
	return w, h, fit * v.scale
}

func (v *imageViewerState) clampPan() {
	w, h, _ := v.shown()
	maxX, maxY := max(0, (w-v.viewW)/2), max(0, (h-v.viewH)/2)
	v.panX = min(max(v.panX, -maxX), maxX)
	v.panY = min(max(v.panY, -maxY), maxY)
}

func (v *imageViewerState) maxScale() float32 {
	fit := v.fitScale()
	if fit <= 0 {
		return viewerMaxZoom
	}
	return max(viewerMaxZoom, 1/fit)
}

// zoomTo enlarges around the point at x, y, which keeps what is under the
// pointer where it is, as a pinch does.
func (v *imageViewerState) zoomTo(scale, x, y float32) {
	scale = min(max(scale, 1), v.maxScale())
	if scale == v.scale {
		return
	}
	oldW, oldH, _ := v.shown()
	cx, cy := v.viewW/2+v.panX, v.viewH/2+v.panY
	kx, ky := (x-cx)/max(oldW, 1), (y-cy)/max(oldH, 1)
	v.scale = scale
	newW, newH, _ := v.shown()
	v.panX = x - v.viewW/2 - kx*newW
	v.panY = y - v.viewH/2 - ky*newH
	v.clampPan()
}

func (v *imageViewerState) zoomBy(factor float32) {
	v.zoomTo(v.scale*factor, v.viewW/2+v.panX, v.viewH/2+v.panY)
}

func (v *imageViewerState) fit() {
	v.scale, v.panX, v.panY = 1, 0, 0
}

func (v *imageViewerState) actual() {
	fit := v.fitScale()
	if fit <= 0 {
		return
	}
	v.zoomTo(1/fit, v.viewW/2+v.panX, v.viewH/2+v.panY)
}

// toggleFit switches between the fitted picture and its own pixels, as a
// double click does in a viewer.
func (v *imageViewerState) toggleFit() {
	if v.scale > 1.001 {
		v.fit()
		return
	}
	v.actual()
}

func (v *imageViewerState) input(c *ui.Context, ev ui.InputEvent) bool {
	if v.bmp == nil {
		return false
	}
	switch ev.Kind {
	case ui.InputZoom:
		v.zoomTo(v.scale*ev.Scale, ev.X, ev.Y)
		c.Invalidate()
		return true
	case ui.InputScroll:
		if ev.Mods&(ui.Ctrl|ui.Cmd) != 0 || !ev.Precise {
			// A wheel turns in notches, which come as lines rather than
			// pixels: one notch is one step, and a pinch a share of its own.
			factor := float32(math.Exp(-float64(ev.DY) * viewerWheelZoom))
			if !ev.Precise {
				switch {
				case ev.DY > 0:
					factor = 1 / viewerWheelStep
				case ev.DY < 0:
					factor = viewerWheelStep
				}
			}
			v.zoomTo(v.scale*factor, ev.X, ev.Y)
		} else {
			// A trackpad pans, as it does in a picture: the view follows the
			// fingers.
			v.panX -= ev.DX
			v.panY -= ev.DY
			v.clampPan()
		}
		c.Invalidate()
		return true
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		if ev.Clicks >= 2 {
			v.toggleFit()
			c.Invalidate()
			return true
		}
		w, h, _ := v.shown()
		if w <= v.viewW && h <= v.viewH {
			// Nothing to drag while the whole picture shows.
			return false
		}
		v.dragging = true
		v.dragX, v.dragY = ev.X, ev.Y
		v.dragPanX, v.dragPanY = v.panX, v.panY
		c.Invalidate()
		return true
	case ui.InputPointerMove:
		if !v.dragging {
			return false
		}
		v.panX = v.dragPanX + ev.X - v.dragX
		v.panY = v.dragPanY + ev.Y - v.dragY
		v.clampPan()
		c.Invalidate()
		return true
	case ui.InputPointerUp:
		if !v.dragging {
			return false
		}
		v.dragging = false
		c.Invalidate()
		return true
	}
	return false
}

func (v *imageViewerState) view(c *ui.Context) {
	t := c.Theme()
	if c.Shortcut(0, ui.KeyEscape) {
		if v.win != nil {
			v.win.Close()
		}
		return
	}
	if c.Shortcut(0, ui.KeyMinus) {
		v.zoomBy(1 / viewerStep)
		c.Invalidate()
	}
	if c.Shortcut(0, ui.KeyEqual) {
		v.zoomBy(viewerStep)
		c.Invalidate()
	}
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		viewport := ui.Box(c.Key("viewport")).Grow(1).FillWidth().Clip().Background(t.Background)
		if box := viewport.Bounds(); box.W > 0 && box.H > 0 {
			if v.viewW != box.W || v.viewH != box.H {
				v.viewW, v.viewH = box.W, box.H
				// A resized window keeps the picture inside it.
				v.clampPan()
			}
		} else {
			c.Invalidate()
		}
		// The painter knows how many device pixels a DIP is, which is what
		// makes "actual size" the picture's own pixels rather than a guess.
		viewport.Draw(func(p *ui.Painter, r ui.Rect) {
			if scale := p.Scale(); scale > 0 && (!v.dpiKnown || scale != v.dpi) {
				v.dpi, v.dpiKnown = scale, true
			}
		})
		grabbing := v.dragging
		if w, h, _ := v.shown(); w > v.viewW || h > v.viewH {
			if grabbing {
				viewport.Cursor(ui.CursorGrabbing)
			} else {
				viewport.Cursor(ui.CursorGrab)
			}
		}
		viewport.HandleInput(func(ev ui.InputEvent) bool { return v.input(c, ev) })
		viewport.Children(func() {
			switch {
			case v.err != "":
				ui.Column(c).Fill().Center().Children(func() {
					ui.Text(c, v.app.tr(v.err)).TextColor(t.TextMuted)
				})
			case v.bmp == nil:
				ui.Column(c).Fill().Center().Children(func() {
					ui.Text(c, v.app.tr("正在加载图片…")).TextColor(t.TextMuted)
				})
			default:
				// Placed rather than centered: past fitting, the picture is
				// larger than the room it has, and where it sits is the pan.
				w, h, _ := v.shown()
				ui.Image(c, v.bmp).Absolute().
					Left((v.viewW-w)/2 + v.panX).Top((v.viewH-h)/2 + v.panY).
					Width(w).Height(h).IgnoreInput(true)
			}
		})
		v.toolbar(c)
	})
}

func (v *imageViewerState) toolbar(c *ui.Context) {
	t := c.Theme()
	_, _, share := v.shown()
	// Before the first paint the screen's scale is unknown, and a share of
	// the picture's pixels would be a guess: show nothing rather than a
	// number that changes a frame later.
	percent := ""
	if v.bmp != nil && v.dpiKnown {
		percent = fmt.Sprintf("%d%%", max(int(math.Round(float64(share)*100)), 1))
	}
	ui.Row(c).FillWidth().Padding(10, 12).Gap(8).AlignItems(ui.Center).
		Background(t.Surface).InsetShadow(0, 1, 0, 0, t.Border).Children(func() {
		if ui.Button(c, "−").Label(v.app.tr("缩小")).Tooltip(v.app.tr("缩小")).Disabled(v.bmp == nil || v.scale <= 1).Clicked() {
			v.zoomBy(1 / viewerStep)
			c.Invalidate()
		}
		ui.Text(c, percent).FontSize(12).FontFeatures("tnum").
			MinWidth(44).TextAlign(ui.Center).Tooltip(v.app.tr("相对图片原始像素"))
		if ui.Button(c, "+").Label(v.app.tr("放大")).Tooltip(v.app.tr("放大")).Disabled(v.bmp == nil || v.scale >= v.maxScale()).Clicked() {
			v.zoomBy(viewerStep)
			c.Invalidate()
		}
		if ui.Button(c, v.app.tr("适应窗口")).Disabled(v.bmp == nil || v.scale <= 1).Clicked() {
			v.fit()
			c.Invalidate()
		}
		if ui.Button(c, v.app.tr("实际大小")).Disabled(v.bmp == nil || math.Abs(float64(share)-1) < 0.005).Clicked() {
			v.actual()
			c.Invalidate()
		}
		ui.Spacer(c)
		if v.note != "" {
			ui.Text(c, v.app.tr(v.note)).FontSize(12).TextColor(t.Danger).SingleLine().Tooltip(v.app.tr(v.note))
		}
		if ui.Button(c, v.app.tr("用系统应用打开")).Label(v.app.tr("用系统应用打开原图")).
			Disabled(v.bmp == nil || v.opening).Clicked() {
			v.openInSystem()
		}
		if ui.Button(c, v.app.tr("关闭")).Clicked() {
			if v.win != nil {
				v.win.Close()
			}
		}
	})
}

// openInSystem hands the staged original to the system viewer, which can
// annotate it and zoom further than this window does. A viewer that closed on
// the way launches nothing.
func (v *imageViewerState) openInSystem() {
	if v.opening || v.closed {
		return
	}
	request := &struct{}{}
	v.openRequest, v.opening, v.note = request, true, ""
	go func() {
		err := v.stageAndOpenSystem()
		mygo.RunOnMain(func() { v.finishSystemOpen(request, err) })
	}()
}

// finishSystemOpen ends one launch. A result for one the window has moved on
// from changes nothing, and a closed window is not repainted.
func (v *imageViewerState) finishSystemOpen(request *struct{}, err error) {
	if v.openRequest != request {
		return
	}
	v.openRequest, v.opening = nil, false
	if v.closed {
		return
	}
	if err != nil {
		v.note = "用系统应用打开失败，请重试"
	}
	if v.win != nil {
		v.win.Invalidate()
	}
}

// stageAndOpenSystem copies the original out of the history and hands it to
// the system viewer, which has imageOpenTimeout to take it. Both steps read
// the disk and leave the process, so they run off the UI thread.
func (v *imageViewerState) stageAndOpenSystem() error {
	path, err := v.app.hist.StageImage(v.id)
	if err != nil {
		return err
	}
	ctx := v.app.root
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, imageOpenTimeout)
	defer cancel()
	return openSystemImage(ctx, path)
}
