package resources

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

// The sidebar logo is one small picture. Embedding the full size artwork
// behind it would put a megabyte of pixels in the binary for a 28 DIP header.
func TestLogoIsASmallSquarePicture(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(LogoPNG))
	if err != nil {
		t.Fatal(err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != bounds.Dy() {
		t.Fatalf("logo is %d×%d, want a square so it fits the header box", bounds.Dx(), bounds.Dy())
	}
	if bounds.Dx() < 96 {
		t.Fatalf("logo is %d pixels wide, too few for a 3× display", bounds.Dx())
	}
	if len(LogoPNG) > 64<<10 {
		t.Fatalf("logo is %d bytes, want a small asset rather than the source artwork", len(LogoPNG))
	}
	_, _, _, alpha := img.At(bounds.Min.X, bounds.Min.Y).RGBA()
	if alpha != 0 {
		t.Fatal("logo corners are opaque, so it cannot sit on the sidebar")
	}
	if _, ok := img.(*image.NRGBA); !ok {
		t.Fatalf("logo decodes to %T, want NRGBA with straight alpha", img)
	}
}
