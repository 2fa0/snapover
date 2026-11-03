package clip

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

func TestDIBInitializesHeaderInReusedNativeMemory(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	img.SetNRGBA(0, 1, color.NRGBA{B: 255, A: 255})
	img.SetNRGBA(1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	// GlobalAlloc(GMEM_MOVEABLE) does not zero the destination like Go's make.
	raw := bytes.Repeat([]byte{0xa5}, dibRawSize(2, 2))
	encodeDIBInto(raw, img)
	for _, offset := range []int{16, 24, 28, 32, 36} {
		if value := binary.LittleEndian.Uint32(raw[offset:]); value != 0 {
			t.Fatalf("DIB header at offset %d retained native memory: %#x", offset, value)
		}
	}
	back, err := decodeDIB(raw)
	if err != nil {
		t.Fatal(err)
	}
	if HashImage(img) != HashImage(back) {
		t.Fatal("DIB in reused native memory changed colors or row order")
	}
}

func TestDIBColorAndRowOrder(t *testing.T) {
	img := image.NewNRGBA(image.Rect(2, 3, 5, 5))
	for y := 3; y < 5; y++ {
		for x := 2; x < 5; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: byte(30 * x), G: byte(20 * y), B: 90, A: 255})
		}
	}
	raw := encodeDIB(img)
	if bits := binary.LittleEndian.Uint16(raw[14:]); bits != 24 {
		t.Fatalf("compatible DIB should be 24-bit, got %d", bits)
	}
	if height := int32(binary.LittleEndian.Uint32(raw[8:])); height != 2 {
		t.Fatalf("compatible DIB should be bottom-up, got height %d", height)
	}
	back, err := decodeDIB(raw)
	if err != nil {
		t.Fatal(err)
	}
	if HashImage(img) != HashImage(back) {
		t.Fatal("DIB changed colors or row order")
	}
	// Convert the bottom-up payload to top-down without changing the picture.
	binary.LittleEndian.PutUint32(raw[8:], ^uint32(1))
	a, b := append([]byte(nil), raw[40:52]...), append([]byte(nil), raw[52:64]...)
	copy(raw[40:], b)
	copy(raw[52:], a)
	back, err = decodeDIB(raw)
	if err != nil || HashImage(img) != HashImage(back) {
		t.Fatal("top-down DIB changed", err)
	}
}
func TestDIBRejectsInvalidDimensionsLayoutAndMasks(t *testing.T) {
	good := encodeDIB(image.NewNRGBA(image.Rect(0, 0, 2, 2)))
	for _, mutate := range []func([]byte) []byte{
		func(b []byte) []byte { return b[:39] },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b[4:], 0x7fffffff); return b },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b[8:], 0x80000000); return b },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b, 400); return b },
		func(b []byte) []byte { binary.LittleEndian.PutUint16(b[14:], 8); return b },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b[16:], 3); return b },
		func(b []byte) []byte { return b[:len(b)-1] },
	} {
		b := mutate(append([]byte(nil), good...))
		if _, err := decodeDIB(b); err == nil {
			t.Fatal("invalid DIB accepted")
		}
	}
}
func TestDecodedImageMemoryBudget(t *testing.T) {
	if err := checkImageConfig(image.Config{Width: 4000, Height: 10000, ColorModel: color.NRGBAModel}); err != nil {
		t.Fatal(err)
	}
	if err := checkImageConfig(image.Config{Width: 4000, Height: 10000, ColorModel: color.NRGBA64Model}); err == nil {
		t.Fatal("16-bit pixels exceeded decoded-memory budget")
	}
}

func TestDIB24AndV5Alpha(t *testing.T) {
	raw := make([]byte, 48)
	le := binary.LittleEndian
	le.PutUint32(raw, 40)
	le.PutUint32(raw[4:], 1)
	le.PutUint32(raw[8:], 2)
	le.PutUint16(raw[12:], 1)
	le.PutUint16(raw[14:], 24)
	copy(raw[40:], []byte{255, 0, 0, 0, 0, 0, 255, 0})
	img, err := decodeDIB(raw)
	if err != nil {
		t.Fatal(err)
	}
	if img.NRGBAAt(0, 0) != (color.NRGBA{R: 255, A: 255}) || img.NRGBAAt(0, 1) != (color.NRGBA{B: 255, A: 255}) {
		t.Fatal("24-bit padded rows changed")
	}
	v5 := make([]byte, 128)
	le.PutUint32(v5, 124)
	le.PutUint32(v5[4:], 1)
	le.PutUint32(v5[8:], ^uint32(0))
	le.PutUint16(v5[12:], 1)
	le.PutUint16(v5[14:], 32)
	le.PutUint32(v5[16:], 3)
	le.PutUint32(v5[40:], 0xff0000)
	le.PutUint32(v5[44:], 0xff00)
	le.PutUint32(v5[48:], 0xff)
	le.PutUint32(v5[52:], 0xff000000)
	copy(v5[124:], []byte{1, 2, 3, 128})
	img, err = decodeDIB(v5)
	if err != nil || img.NRGBAAt(0, 0) != (color.NRGBA{R: 3, G: 2, B: 1, A: 128}) {
		t.Fatal("V5 alpha changed", err)
	}
}

func TestDIB24BottomUpPadsOddWidth(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})
	img.SetNRGBA(0, 1, color.NRGBA{B: 255, A: 64})
	raw := bytes.Repeat([]byte{0xa5}, dibRawSize(1, 2))
	encodeDIBInto(raw, img)
	if len(raw) != 48 {
		t.Fatalf("odd-width 24-bit DIB size %d", len(raw))
	}
	if raw[43] != 0 || raw[47] != 0 {
		t.Fatal("row padding retained native memory")
	}
	back, err := decodeDIB(raw)
	if err != nil {
		t.Fatal(err)
	}
	if back.NRGBAAt(0, 0) != (color.NRGBA{R: 255, A: 255}) || back.NRGBAAt(0, 1) != (color.NRGBA{B: 255, A: 255}) {
		t.Fatal("24-bit DIB dropped opaque RGB or kept alpha")
	}
}

func encodeDIB(img image.Image) []byte {
	b := img.Bounds()
	raw := make([]byte, dibRawSize(b.Dx(), b.Dy()))
	encodeDIBInto(raw, img)
	return raw
}
