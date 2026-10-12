package clip

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"

	"github.com/zeebo/blake3"
)

func legacyImageHash(img image.Image) [32]byte {
	b := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	h := blake3.New()
	var dim [8]byte
	binary.LittleEndian.PutUint32(dim[:4], uint32(b.Dx()))
	binary.LittleEndian.PutUint32(dim[4:], uint32(b.Dy()))
	h.Write(dim[:])
	h.Write(dst.Pix)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func TestHashImageMatchesLegacyPixels(t *testing.T) {
	bounds := image.Rect(-2, 3, 11, 12)
	nrgba := image.NewNRGBA(bounds)
	rgba := image.NewRGBA(bounds)
	gray := image.NewGray16(bounds)
	nrgba64 := image.NewNRGBA64(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBA{R: uint8(x * 31), G: uint8(y * 17), B: 231, A: uint8((x + y) * 19)}
			nrgba.SetNRGBA(x, y, c)
			rgba.Set(x, y, c)
			gray.Set(x, y, c)
			nrgba64.Set(x, y, c)
		}
	}
	for _, img := range []image.Image{nrgba, rgba, gray, nrgba64,
		nrgba.SubImage(image.Rect(1, 5, 6, 9)), rgba.SubImage(image.Rect(1, 5, 6, 9)),
		image.NewNRGBA(image.Rectangle{})} {
		if got, want := HashImage(img), legacyImageHash(img); got != want {
			t.Fatalf("hash mismatch for %T bounds %v", img, img.Bounds())
		}
	}
}

func TestFromPNGBoundedThumbnail(t *testing.T) {
	for _, bounds := range []image.Rectangle{image.Rect(0, 0, 400, 1000), image.Rect(0, 0, 1000, 1)} {
		var raw bytes.Buffer
		if err := png.Encode(&raw, image.NewNRGBA(bounds)); err != nil {
			t.Fatal(err)
		}
		item, err := FromPNG(raw.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		thumb, err := png.Decode(bytes.NewReader(item.Thumbnail))
		if err != nil {
			t.Fatal(err)
		}
		b := thumb.Bounds()
		if b.Dx() < 1 || b.Dy() < 1 || b.Dx() > 160 || b.Dy() > 160 {
			t.Fatalf("thumbnail bounds: %v", b)
		}
		if item.Width != bounds.Dx() || item.Height != bounds.Dy() {
			t.Fatal("original dimensions changed")
		}
	}
}

func BenchmarkFromPNGLarge(b *testing.B) {
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 4000, 10000))); err != nil {
		b.Fatal(err)
	}
	data := raw.Bytes()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := FromPNG(data); err != nil {
			b.Fatal(err)
		}
	}
}
