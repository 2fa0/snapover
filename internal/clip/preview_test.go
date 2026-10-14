package clip

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestPreviewResolutionAndPixelBudget(t *testing.T) {
	for _, tc := range []struct{ w, h, wantW, wantH int }{
		{1791, 438, 1120, 274},
		{1400, 1400, 680, 680},
		{400, 1000, 272, 680},
		{80, 40, 80, 40},
	} {
		data, err := PreviewPNG(image.NewNRGBA(image.Rect(0, 0, tc.w, tc.h)))
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Width != tc.wantW || cfg.Height != tc.wantH || cfg.Width*cfg.Height*4*4/3 > 4<<20 {
			t.Fatalf("preview of %dx%d: %+v", tc.w, tc.h, cfg)
		}
	}
}

func TestPreviewResamplingRetainsThinDetails(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2240, 8))
	for y := range 8 {
		for x := range 2240 {
			v := uint8(0)
			if x%2 == 0 {
				v = 255
			}
			img.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: 255})
		}
	}
	data, err := PreviewPNG(img)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r, _, _, _ := preview.At(10, 1).RGBA()
	if r < 100*257 || r > 155*257 {
		t.Fatalf("thin details were lost instead of averaged: %d", r/257)
	}
}
