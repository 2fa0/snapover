package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"testing"

	"github.com/2fa0/snapover/resources"
)

func TestPickerShortcuts(t *testing.T) {
	got := pickerShortcuts()
	if runtime.GOOS == "darwin" {
		if len(got) != 2 || got[0] != "Alt+Cmd+V" {
			t.Fatalf("darwin shortcuts = %v", got)
		}
		return
	}
	if len(got) != 1 || got[0] != "Alt+V" {
		t.Fatalf("shortcuts = %v", got)
	}
}

func TestMenuPNGBecomesTrayIcon(t *testing.T) {
	out, err := trayIconPNG(resources.IconPNG, false)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	var opaque int
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a > 0x2000 {
				opaque++
			}
		}
	}
	if opaque < 20 || opaque > 700 {
		t.Fatalf("opaque pixels = %d", opaque)
	}
}

func TestTrayIconPreservesAlphaAndAspectRatio(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 100})
		}
	}
	for y := 1; y < 3; y++ {
		for x := 2; x < 6; x++ {
			pixel := color.NRGBA{A: 255}
			if x >= 4 {
				pixel = color.NRGBA{A: 128}
			}
			src.SetNRGBA(x, y, pixel)
		}
	}
	src.SetNRGBA(2, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	src.SetNRGBA(5, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 128})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	for _, white := range []bool{false, true} {
		out, err := trayIconPNG(buf.Bytes(), white)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds() != image.Rect(0, 0, 32, 32) {
			t.Fatalf("bounds = %v", img.Bounds())
		}
		if bounds := cropAlpha(img).Bounds(); bounds.Dx() != 26 || bounds.Dy() != 13 {
			t.Fatalf("bird bounds = %v, want 26x13", bounds)
		}
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Fatal("corner should be transparent")
		}
		for _, x := range []int{5, 24} {
			if _, _, _, a := img.At(x, 11).RGBA(); a != 0 {
				t.Fatal("white feather detail should be transparent")
			}
		}
		var ink uint8
		if white {
			ink = 255
		}
		for _, sample := range []struct {
			x     int
			alpha uint8
		}{{8, 255}, {23, 128}} {
			got := color.NRGBAModel.Convert(img.At(sample.x, 16)).(color.NRGBA)
			want := color.NRGBA{R: ink, G: ink, B: ink, A: sample.alpha}
			if got != want {
				t.Fatalf("white=%v pixel(%d,16) = %v, want %v", white, sample.x, got, want)
			}
		}
	}
}
