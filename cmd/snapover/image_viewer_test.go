package main

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func testViewer(t *testing.T, w, h int) (*imageViewerState, *ui.Tester) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.NRGBA{R: 0xff, A: 0xff})
	v := &imageViewerState{app: &app{}, bmp: ui.NewBitmap(img), width: w, height: h, scale: 1, dpi: 1}
	tt := ui.NewTester(v.view, 640, 480)
	tt.Frame()
	tt.Frame()
	return v, tt
}

// imagePointAt is where the pointer rests on the picture, as a share of its
// width and height from the middle.
func imagePointAt(v *imageViewerState, x, y float32) (float32, float32) {
	w, h, _ := v.shown()
	cx, cy := v.viewW/2+v.panX, v.viewH/2+v.panY
	return (x - cx) / max(w, 1), (y - cy) / max(h, 1)
}

func TestViewerFitsTheWholePicture(t *testing.T) {
	v, _ := testViewer(t, 1200, 600)
	w, h, share := v.shown()
	if v.scale != 1 || share >= 1 {
		t.Fatalf("a large picture opened at scale %v, share %v", v.scale, share)
	}
	if w > v.viewW-viewerMargin || h > v.viewH-viewerMargin {
		t.Fatalf("the fitted picture %vx%v overflows the window %vx%v", w, h, v.viewW, v.viewH)
	}
	if math.Abs(float64(w/h)-2) > 0.01 {
		t.Fatalf("fitting changed the aspect ratio: %vx%v", w, h)
	}
}

func TestViewerDoesNotBlowUpASmallPicture(t *testing.T) {
	v, _ := testViewer(t, 80, 40)
	if w, h, share := v.shown(); share != 1 || w != 80 || h != 40 {
		t.Fatalf("a small picture was enlarged to %vx%v at %v", w, h, share)
	}
}

func TestViewerPinchKeepsThePointUnderThePointer(t *testing.T) {
	v, tt := testViewer(t, 1200, 600)
	atX, atY := float32(200), float32(150)
	beforeX, beforeY := imagePointAt(v, atX, atY)
	tt.Pinch(atX, atY, 2)
	tt.Frame()
	if v.scale <= 1 {
		t.Fatalf("pinching did not enlarge: scale %v", v.scale)
	}
	afterX, afterY := imagePointAt(v, atX, atY)
	if math.Abs(float64(beforeX-afterX)) > 0.01 || math.Abs(float64(beforeY-afterY)) > 0.01 {
		t.Fatalf("the picture moved under the pointer: %v,%v became %v,%v", beforeX, beforeY, afterX, afterY)
	}
}

func TestViewerActualSizeShowsOnePixelPerPixel(t *testing.T) {
	v, _ := testViewer(t, 1200, 600)
	v.dpi = 2
	v.actual()
	if _, _, share := v.shown(); math.Abs(float64(share)-1) > 0.01 {
		t.Fatalf("actual size showed %v of the picture", share)
	}
	if w, h, _ := v.shown(); math.Abs(float64(w-600)) > 0.5 || math.Abs(float64(h-300)) > 0.5 {
		t.Fatalf("one image pixel to two device pixels is %vx%v DIPs, want 600x300", w, h)
	}
	v.fit()
	if v.scale != 1 || v.panX != 0 || v.panY != 0 {
		t.Fatalf("fitting left scale %v at %v,%v", v.scale, v.panX, v.panY)
	}
}

func TestViewerNeverZoomsBelowFitOrPastItsLimit(t *testing.T) {
	v, _ := testViewer(t, 1200, 600)
	v.zoomBy(0.25)
	if v.scale != 1 {
		t.Fatalf("zooming out left the fitted picture at %v", v.scale)
	}
	v.zoomBy(1000)
	if got, want := v.scale, v.maxScale(); math.Abs(float64(got-want)) > 0.001 {
		t.Fatalf("zoomed to %v past the limit %v", got, want)
	}
	if _, _, share := v.shown(); share < 1 {
		t.Fatalf("the limit shows %v of the picture, less than actual size", share)
	}
}

func TestViewerDragsOnlyAnEnlargedPicture(t *testing.T) {
	v, tt := testViewer(t, 1200, 600)
	tt.Press(300, 200)
	tt.Move(340, 240)
	tt.Release(340, 240)
	if v.dragging || v.panX != 0 || v.panY != 0 {
		t.Fatalf("the fitted picture moved: %v,%v", v.panX, v.panY)
	}
	tt.Pinch(320, 240, 3)
	tt.Frame()
	startX, startY := v.panX, v.panY
	tt.Press(320, 240)
	tt.Move(280, 210)
	tt.Release(280, 210)
	tt.Frame()
	if v.panX == startX && v.panY == startY {
		t.Fatal("dragging an enlarged picture did not move it")
	}
	if v.dragging {
		t.Fatal("the drag was not let go")
	}
}

func TestViewerClampsPanToTheWindow(t *testing.T) {
	v, _ := testViewer(t, 1200, 600)
	v.zoomBy(4)
	v.panX, v.panY = 100000, -100000
	v.clampPan()
	w, h, _ := v.shown()
	if wantX := (w - v.viewW) / 2; math.Abs(float64(v.panX-wantX)) > 0.5 {
		t.Fatalf("pan x=%v, want %v", v.panX, wantX)
	}
	if wantY := -(h - v.viewH) / 2; math.Abs(float64(v.panY-wantY)) > 0.5 {
		t.Fatalf("pan y=%v, want %v", v.panY, wantY)
	}
}

func TestViewerBoundsWhatItKeepsDecoded(t *testing.T) {
	big := image.NewNRGBA(image.Rect(0, 0, 3600, 3000)) // 10.8 MP, over the bound
	got := boundedViewerImage(big)
	b := got.Bounds()
	if pixels := b.Dx() * b.Dy(); pixels > viewerMaxPixels {
		t.Fatalf("kept %d pixels decoded, over the %d bound", pixels, viewerMaxPixels)
	}
	if math.Abs(float64(b.Dx())/float64(b.Dy())-1.2) > 0.01 {
		t.Fatalf("scaling changed the aspect ratio: %dx%d", b.Dx(), b.Dy())
	}
	small := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	if got := boundedViewerImage(small); got != small {
		t.Fatal("a small picture was copied instead of shown as it is")
	}
}

func TestViewerReportsAFailedRead(t *testing.T) {
	v := &imageViewerState{app: &app{}, err: "图片读取失败，请重新复制后再试", scale: 1, dpi: 1}
	tt := ui.NewTester(v.view, 640, 480)
	tt.Frame()
	if !tt.HasText("图片读取失败，请重新复制后再试") {
		t.Fatal("the viewer did not say the picture could not be read")
	}
}

func TestViewerReleasesItsPixelsWhenClosed(t *testing.T) {
	a, v := imageViewerTestApp(t)
	v.bmp = ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 4, 4)))
	a.imageViewer = v
	request := &struct{ id string }{v.id}
	v.request = request
	a.closeImageViewer()
	v.finishImageLoad(request, ui.NewBitmap(image.NewNRGBA(image.Rect(0, 0, 4, 4))), "")
	if v.bmp != nil {
		t.Fatal("the closed viewer kept its pixels")
	}
	if !v.closed || v.openRequest != nil {
		t.Fatal("the closed viewer still accepts work")
	}
	if a.imageViewer != nil {
		t.Fatal("the app still points at the closed viewer")
	}
	if v.ctx.Err() == nil || v.request != nil {
		t.Fatal("closing did not cancel the viewer's pending work")
	}
}
