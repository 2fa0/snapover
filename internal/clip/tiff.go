package clip

import (
	"bytes"
	"fmt"

	"golang.org/x/image/tiff"
)

func fromTIFF(raw []byte) (Item, error) {
	release := beginImageWork()
	defer release()
	cfg, err := tiff.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return Item{}, fmt.Errorf("读取 TIFF: %w", err)
	}
	if err := checkImageConfig(cfg); err != nil {
		return Item{}, err
	}
	img, err := tiff.Decode(bytes.NewReader(raw))
	if err != nil {
		return Item{}, fmt.Errorf("解码 TIFF: %w", err)
	}
	return itemFromImage(img)
}
