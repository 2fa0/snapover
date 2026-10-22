//go:build darwin

package clip

import "errors"

func readClipboardImage() (Item, error) {
	// Native provider access, byte copies and decoding all stay off the UI.
	for _, format := range []struct {
		typ   string
		limit int
	}{{"public.png", MaxImageBytes}, {"public.tiff", MaxNativeImageBytes}} {
		raw, err := readNativePasteboardData(format.typ, format.limit)
		if err != nil {
			return Item{}, err
		}
		if len(raw) == 0 {
			continue
		}
		if format.typ == "public.png" {
			return FromPNG(raw)
		}
		return fromTIFF(raw)
	}
	return Item{}, errors.New("剪贴板没有可读取的图片")
}
