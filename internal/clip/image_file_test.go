package clip

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestImageFileConvertsJPEGAndPreservesPNG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	img.SetNRGBA(1, 1, color.NRGBA{R: 100, G: 50, B: 10, A: 255})
	dir := t.TempDir()
	for _, format := range []string{"png", "jpeg"} {
		var data bytes.Buffer
		var err error
		if format == "png" {
			err = png.Encode(&data, img)
		} else {
			err = jpeg.Encode(&data, img, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "图片."+format)
		if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		item, err := FromImageFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if item.Width != 4 || item.Height != 3 || item.Kind != KindImage {
			t.Fatalf("%+v", item)
		}
		decoded, err := FromPNG(item.PNG)
		if err != nil || decoded.Hash != item.Hash {
			t.Fatalf("invalid output PNG: %v", err)
		}
		if format == "png" && !bytes.Equal(item.PNG, data.Bytes()) {
			t.Fatal("PNG was unnecessarily re-encoded")
		}
	}
	if _, err := FromImageFile(dir); err == nil {
		t.Fatal("directory accepted as image")
	}
	bad := filepath.Join(dir, "bad.jpg")
	if err := os.WriteFile(bad, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FromImageFile(bad); err == nil {
		t.Fatal("invalid image accepted")
	}
}
