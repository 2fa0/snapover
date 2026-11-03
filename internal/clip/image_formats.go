package clip

import "errors"

// imageFlavor names one native clipboard representation of an image. The name
// differs per platform ("PNG" and CF_DIB on Windows, public.png and public.tiff
// on macOS), so the platform files map a flavor to its native format id.
type imageFlavor int

const (
	// flavorPNG is a PNG byte stream: Windows registered format "PNG" and the
	// macOS public.png type. It is the only representation every platform both
	// writes and reads with straight alpha intact.
	flavorPNG imageFlavor = iota
	// flavorPNGMime is the same PNG payload under another Windows registered
	// format name. Browsers register either spelling.
	flavorPNGMime
	// flavorDIBV5 is a BITMAPV5HEADER device independent bitmap. Its alpha is
	// trustworthy only when bV5AlphaMask is set.
	flavorDIBV5
	// flavorDIB is a BITMAPINFOHEADER bitmap: no file header, bottom-up rows,
	// 4-byte aligned stride. Most producers leave it 24-bit and drop alpha.
	flavorDIB
	// flavorTIFF is the AppKit native image type, published as public.tiff.
	flavorTIFF
)

// errNoClipboardImage reports that no offered format held a usable image.
var errNoClipboardImage = errors.New("剪贴板没有可读取的图片")

func (f imageFlavor) String() string {
	switch f {
	case flavorPNG:
		return "PNG"
	case flavorPNGMime:
		return "image/png"
	case flavorDIBV5:
		return "CF_DIBV5"
	case flavorDIB:
		return "CF_DIB"
	case flavorTIFF:
		return "public.tiff"
	}
	return "unknown"
}

// windowsImageReadFlavors is the Windows read order. PNG is first because only
// PNG is guaranteed to carry straight alpha. CF_DIBV5 follows because an
// explicit alpha mask can be trusted, and CF_DIB is last because a 24-bit or
// mask-less 32-bit DIB cannot express transparency at all.
//
// CF_DIBV5 must only be read when the clipboard really offers it. Otherwise the
// system repackages an opaque CF_DIB as a DIBV5, which loses alpha silently and
// on Windows 11 can leave the clipboard in a state where other applications
// paste a pixel-shifted image (QTBUG-104872).
var windowsImageReadFlavors = []imageFlavor{flavorPNG, flavorPNGMime, flavorDIBV5, flavorDIB}

// windowsImageWriteFlavors is the exact set published on Windows, and the reason
// this package does not simply hand images to MyGo: the pair below is a
// compatibility requirement, not a preference.
//
// Feishu and similar applications convert CF_DIB to CF_BITMAP, which rejects a
// top-down 32-bit payload, so CF_DIB stays 24-bit bottom-up. That DIB cannot
// carry alpha, so PNG must stay next to it and is what every alpha-aware target
// reads. Do not add CF_DIBV5, CF_BITMAP or a TIFF: the first reintroduces the
// rejected 32-bit form, and the others have no consumer on Windows.
var windowsImageWriteFlavors = []imageFlavor{flavorPNG, flavorDIB}

// darwinImageReadFlavors is the macOS read order with the byte cap for each
// type. PNG comes first, but TIFF is not optional: Safari publishes an image
// copy as public.tiff and offers no public.png at all, while Preview adds both.
var darwinImageReadFlavors = []struct {
	name   string
	flavor imageFlavor
	limit  int
}{
	{"public.png", flavorPNG, MaxImageBytes},
	{"public.tiff", flavorTIFF, MaxNativeImageBytes},
}

// imageFormatNames are clipboard format names that mean "this snapshot is an
// image". Detection is wider than what either platform can decode, and that is
// deliberate: an image the reader cannot handle must report a skip reason
// instead of looking like a copy with nothing to sync.
var imageFormatNames = []string{
	"image/png", "image/tiff", "image/bmp", "image/jpeg", "image/jpg",
	"image/gif", "image/webp", "image/heic", "image/heif", "image/avif",
	"png", "tiff", "bmp", "jpeg", "jpg", "gif", "webp",
	"public.png", "public.tiff", "public.bmp", "public.jpeg",
	"public.heic", "public.heif", "public.avif", "public.webp",
	"com.compuserve.gif", "org.webmproject.webp",
}

// decodeImageFlavor turns one native payload into a clipboard item. The caller
// has already bounded the bytes; each decoder still validates the layout.
func decodeImageFlavor(flavor imageFlavor, raw []byte) (Item, error) {
	switch flavor {
	case flavorPNG, flavorPNGMime:
		return FromPNG(raw)
	case flavorTIFF:
		return fromTIFF(raw)
	case flavorDIB, flavorDIBV5:
		// FromPNG and fromTIFF take the image-work slot themselves.
		release := beginImageWork()
		defer release()
		img, err := decodeDIB(raw)
		if err != nil {
			return Item{}, err
		}
		return itemFromImage(img)
	}
	return Item{}, errors.New("未知的剪贴板图片格式")
}
