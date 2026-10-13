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
	if err := checkPixels(cfg.Width, cfg.Height); err != nil {
		return Item{}, err
	}
	if format == "png" {
		return FromPNG(data)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Item{}, fmt.Errorf("解码图片失败：%w", err)
	}
	defer ReclaimImageMemory(cfg.Width, cfg.Height)
	var encoded boundedPNG
	if err := png.Encode(&encoded, img); err != nil {
		return Item{}, err
	}
	thumb, _ := ThumbnailPNG(img, 160)
	return Item{Kind: KindImage, PNG: encoded.Bytes(), Thumbnail: thumb,
		Hash: HashImage(img), Width: cfg.Width, Height: cfg.Height, Size: encoded.Len()}, nil
}

type boundedPNG struct{ bytes.Buffer }

func (b *boundedPNG) Write(p []byte) (int, error) {
	if len(p) > MaxImageBytes-b.Len() {
		return 0, errors.New("图片转为 PNG 后超过 20 MB")
	}
	return b.Buffer.Write(p)
}
