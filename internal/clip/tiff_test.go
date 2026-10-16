package clip

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	"golang.org/x/image/tiff"
)

func TestNativeTIFFBuildsPNGAndMetadataFromSamePixels(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 7, 9))
	img.SetNRGBA(2, 3, color.NRGBA{R: 20, G: 90, B: 40, A: 255})
	var raw bytes.Buffer
	if err := tiff.Encode(&raw, img, nil); err != nil {
		t.Fatal(err)
	}
	item, err := fromTIFF(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if item.Hash != HashImage(img) || len(item.Preview) == 0 || len(item.Thumbnail) == 0 {
		t.Fatal("native TIFF lost image metadata")
	}
	decoded, err := DecodePNG(item.PNG)
	if err != nil || HashImage(decoded) != item.Hash {
		t.Fatal("TIFF conversion changed pixels", err)
	}
}
