package clip

import (
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

// Feishu and similar Windows apps convert CF_DIB to CF_BITMAP, which rejects a
// top-down 32-bit payload. That is why the Windows write set is exactly PNG plus
// a 24-bit bottom-up CF_DIB. This test fails if a refactor reintroduces the
// rejected form or drops the PNG that carries alpha.
func TestWindowsImageWriteFlavorsStayFeishuCompatible(t *testing.T) {
	if len(windowsImageWriteFlavors) != 2 {
		t.Fatalf("Windows writes %v; want exactly [PNG CF_DIB]", windowsImageWriteFlavors)
	}
	var png, dib bool
	for _, flavor := range windowsImageWriteFlavors {
		switch flavor {
		case flavorPNG:
			png = true
		case flavorDIB:
			dib = true
		default:
			t.Fatalf("Windows must not publish %v", flavor)
		}
	}
	if !png || !dib {
		t.Fatalf("Windows writes %v; need PNG for alpha and CF_DIB for CF_BITMAP consumers", windowsImageWriteFlavors)
	}
}

// The 24-bit DIB must keep every BITMAPINFOHEADER field that a CF_DIB to
// CF_BITMAP conversion relies on: 24 bpp, BI_RGB, positive height for bottom-up
// rows, 4-byte aligned stride, and a correct biSizeImage.
func TestEncodedDIBIsFeishuCompatible(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	// Alpha is deliberately not opaque: the 24-bit form drops it, and the
	// remaining colour channels must survive unchanged.
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 0})
	img.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 128})
	img.SetNRGBA(2, 0, color.NRGBA{B: 255, A: 64})
	img.SetNRGBA(0, 1, color.NRGBA{A: 255})

	raw := encodeDIB(img)
	if len(raw) != 40+12*2 {
		t.Fatalf("DIB size %d, want 64 for a 3x2 24-bit bitmap with 12-byte stride", len(raw))
	}
	le := binary.LittleEndian
	if got := le.Uint32(raw); got != 40 {
		t.Fatalf("biSize %d, want a 40-byte BITMAPINFOHEADER", got)
	}
	if got := le.Uint32(raw[4:]); got != 3 {
		t.Fatalf("biWidth %d, want 3", got)
	}
	height := int32(le.Uint32(raw[8:]))
	if height != 2 {
		t.Fatalf("biHeight %d, want +2 for bottom-up rows", height)
	}
	if got := le.Uint16(raw[12:]); got != 1 {
		t.Fatalf("biPlanes %d, want 1", got)
	}
	if got := le.Uint16(raw[14:]); got != 24 {
		t.Fatalf("biBitCount %d, want 24", got)
	}
	if got := le.Uint32(raw[16:]); got != 0 {
		t.Fatalf("biCompression %d, want BI_RGB (0)", got)
	}
	if got := le.Uint32(raw[20:]); got != 12*2 {
		t.Fatalf("biSizeImage %d, want %d", got, 12*2)
	}
	// Bottom-up: image row 0 is the last row in memory, so the first pixel in
	// memory is image (0,1). Row padding must be zeroed, not native residue.
	if got := raw[40:52]; string(got) != string([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("last image row in memory %v, want zeroed black plus padding", got)
	}
	want := []byte{0, 0, 255, 0, 255, 0, 255, 0, 0, 0, 0, 0}
	if got := raw[52:64]; string(got) != string(want) {
		t.Fatalf("first image row in memory %v, want BGR %v", got, want)
	}
}

// PNG has to be tried before a DIB, because only PNG reliably carries the alpha
// channel, and a DIBV5 must be preferred over a DIB when both are real.
func TestWindowsImageReadFlavorsPreferAlphaCapableFormats(t *testing.T) {
	if len(windowsImageReadFlavors) == 0 || windowsImageReadFlavors[0] != flavorPNG {
		t.Fatalf("Windows read order %v, want PNG first", windowsImageReadFlavors)
	}
	order := map[imageFlavor]int{}
	for i, flavor := range windowsImageReadFlavors {
		order[flavor] = i
	}
	dibV5, hasV5 := order[flavorDIBV5]
	dib, hasDIB := order[flavorDIB]
	if !hasV5 || !hasDIB {
		t.Fatalf("Windows read order %v, want both CF_DIBV5 and CF_DIB", windowsImageReadFlavors)
	}
	if dibV5 > dib {
		t.Fatalf("Windows reads CF_DIBV5 at %d and CF_DIB at %d; the alpha-capable form must come first", dibV5, dib)
	}
}

// Safari publishes an image copy as public.tiff with no public.png, so dropping
// TIFF from the macOS read order breaks pasting from Safari.
func TestDarwinImageReadFlavorsIncludeTIFF(t *testing.T) {
	var names []string
	for _, candidate := range darwinImageReadFlavors {
		names = append(names, candidate.name)
		if candidate.limit <= 0 {
			t.Fatalf("%s has no byte cap", candidate.name)
		}
	}
	for _, want := range []string{"public.png", "public.tiff"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("macOS read order %v is missing %s", names, want)
		}
	}
}

// Formats this build cannot decode must still be recognised as images, so the
// copy reports a skip reason instead of looking like nothing happened.
func TestHasImageDetectsFormatsItCannotDecode(t *testing.T) {
	for _, format := range []string{"public.jpeg", "image/jpeg", "com.compuserve.gif", "public.heic", "image/webp"} {
		if !HasImage([]string{format}) {
			t.Fatalf("HasImage(%q) = false; an unreadable image would be skipped silently", format)
		}
	}
	for _, format := range []string{"public.utf8-plain-text", "text/plain", "public.file-url", "image"} {
		if HasImage([]string{format}) {
			t.Fatalf("HasImage(%q) = true, want false", format)
		}
	}
}

// A 32-bit DIBV5 without an alpha mask is read as opaque. Producers such as
// Word and XnView do carry alpha bytes but leave bV5AlphaMask zero, so this is a
// known fidelity limit of the loose cross-device contract, pinned here so it is
// changed deliberately rather than by accident.
func TestDIBV5WithoutAlphaMaskIsOpaque(t *testing.T) {
	raw := make([]byte, 128)
	le := binary.LittleEndian
	le.PutUint32(raw, 124)
	le.PutUint32(raw[4:], 1)
	le.PutUint32(raw[8:], ^uint32(0))
	le.PutUint16(raw[12:], 1)
	le.PutUint16(raw[14:], 32)
	le.PutUint32(raw[20:], 4)
	copy(raw[124:], []byte{1, 2, 3, 200})
	img, err := decodeDIB(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := img.NRGBAAt(0, 0); got != (color.NRGBA{R: 3, G: 2, B: 1, A: 255}) {
		t.Fatalf("pixel %+v, want opaque BGR with alpha dropped", got)
	}
}
