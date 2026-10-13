package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestImageForDisplayReusesPixelsAndPreservesColor(t *testing.T) {
	for _, subimage := range []bool{false, true} {
		src := image.NewNRGBA(image.Rect(0, 0, 256, 256))
		for y := range 256 {
			for x := range 256 {
				src.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(255 - x), B: uint8(x ^ y), A: uint8(y)})
			}
		}
		if subimage {
			src = src.SubImage(image.Rect(3, 5, 253, 255)).(*image.NRGBA)
		}
		want := image.NewRGBA(src.Bounds())
		draw.Draw(want, want.Bounds(), src, src.Bounds().Min, draw.Src)
		got := imageForDisplay(src).(*image.RGBA)
		if &got.Pix[0] != &src.Pix[0] || got.Bounds() != src.Bounds() || got.Stride != src.Stride {
			t.Fatal("display conversion did not reuse the decoded pixels")
		}
		for y := got.Rect.Min.Y; y < got.Rect.Max.Y; y++ {
			start := got.PixOffset(got.Rect.Min.X, y)
			wantStart := want.PixOffset(want.Rect.Min.X, y)
			n := 4 * got.Rect.Dx()
			if !bytes.Equal(got.Pix[start:start+n], want.Pix[wantStart:wantStart+n]) {
				t.Fatalf("display colors changed at row %d (subimage=%v)", y, subimage)
			}
		}
	}
}

func TestImageForDisplayKeepsOtherFormats(t *testing.T) {
	for _, img := range []image.Image{
		image.NewRGBA(image.Rect(0, 0, 3, 2)),
		image.NewGray(image.Rect(0, 0, 3, 2)),
		image.NewNRGBA64(image.Rect(0, 0, 3, 2)),
	} {
		if imageForDisplay(img) != img {
			t.Fatalf("unexpected conversion of %T", img)
		}
	}
}
