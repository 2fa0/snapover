package clip

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// FromImageFile accepts common still-image formats and sends PNG pixels.
func FromImageFile(path string) (Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return Item{}, fmt.Errorf("打开图片失败：%w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Item{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxImageBytes {
		return Item{}, errors.New("请选择 20 MB 以内的普通图片文件")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
	if err != nil {
		return Item{}, err
	}
	if len(data) > MaxImageBytes {
		return Item{}, errors.New("图片超过 20 MB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Item{}, fmt.Errorf("图片格式无效，请使用 PNG、JPEG、BMP 或 WebP：%w", err)
	}
	if err := checkImageConfig(cfg); err != nil {
		return Item{}, err
	}
	if format == "png" {
		return FromPNG(data)
	}
	release := beginImageWork()
	defer release()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Item{}, fmt.Errorf("解码图片失败：%w", err)
	}
	return itemFromImage(img)
}

// The caller holds the image-work budget. Reuse decoded native pixels instead
// of encoding PNG and immediately decoding it a second time for metadata.
func itemFromImage(img image.Image) (Item, error) {
	b := img.Bounds()
	defer ReclaimImageMemory(b.Dx(), b.Dy())
	var encoded boundedPNG
	if err := png.Encode(&encoded, img); err != nil {
		return Item{}, err
	}
	thumb, _ := ThumbnailPNG(img, 160)
	preview, _ := PreviewPNG(img)
	return Item{Kind: KindImage, PNG: encoded.Bytes(), Thumbnail: thumb, Preview: preview,
		Hash: HashImage(img), Width: b.Dx(), Height: b.Dy(), Size: encoded.Len()}, nil
}

type boundedPNG struct{ bytes.Buffer }

func (b *boundedPNG) Write(p []byte) (int, error) {
	if len(p) > MaxImageBytes-b.Len() {
		return 0, errors.New("图片转为 PNG 后超过 20 MB")
	}
	return b.Buffer.Write(p)
}
