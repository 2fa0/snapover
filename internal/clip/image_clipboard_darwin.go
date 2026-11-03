//go:build darwin

package clip

func readClipboardImage() (Item, error) {
	// Native provider access, byte copies and decoding all stay off the UI.
	var decodeErr error
	for _, candidate := range darwinImageReadFlavors {
		raw, err := readNativePasteboardData(candidate.name, candidate.limit)
		if err != nil {
			// A provider that fails or exceeds its cap must not hide another
			// type on the same pasteboard, so keep the reason and try the next.
			if decodeErr == nil {
				decodeErr = err
			}
			continue
		}
		if len(raw) == 0 {
			continue
		}
		item, err := decodeImageFlavor(candidate.flavor, raw)
		if err == nil {
			return item, nil
		}
		if decodeErr == nil {
			decodeErr = err
		}
	}
	if decodeErr != nil {
		return Item{}, decodeErr
	}
	return Item{}, errNoClipboardImage
}
