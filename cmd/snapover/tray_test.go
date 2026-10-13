package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"testing"

	"github.com/2fa0/snapover/internal/trayicon"
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

func TestMenuJPGBecomesTrayIcon(t *testing.T) {
	out, err := trayIconPNG(trayicon.MenuIconJPG, false)
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

func TestTrayIconKnocksOutWhite(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	src.SetNRGBA(3, 3, color.NRGBA{R: 40, G: 180, B: 190, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	out, err := trayIconPNG(buf.Bytes(), false)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 32 {
		t.Fatalf("size = %d", img.Bounds().Dx())
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Fatal("corner should be transparent")
	}
	if _, _, _, a := img.At(16, 16).RGBA(); a == 0 {
		t.Fatal("ink was knocked out")
	}
}
