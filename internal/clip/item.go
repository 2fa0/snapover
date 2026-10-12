// Package clip is the clipboard model for the first sync slice: text, PNG
// images, content hashes, and the echo guard. It does not talk to the network.
package clip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"

	"github.com/zeebo/blake3"
)

var errEmptyWrite = errors.New("没有可写入的内容")

const (
	// MaxTextBytes is the largest text body that will be synced.
	MaxTextBytes = 1 << 20
	// MaxImageBytes is the largest PNG that will be synced.
	MaxImageBytes = 20 << 20
	// MaxPixels rejects decompression bombs. 40e6 covers a 5K screenshot
	// and stops a small PNG that expands to gigabytes.
	MaxPixels = 40_000_000
)

type Kind string

const (
	KindText  Kind = "text"
	KindImage Kind = "image"
)

// Item is one clipboard snapshot. Sensitive and Skip items are not synced.
// Hash is blake3 of the text bytes, or of width||height||NRGBA pixels.
type Item struct {
	Kind      Kind
	Text      string
	PNG       []byte
	Hash      [32]byte
	Width     int
	Height    int
	Size      int
	Sensitive bool
	Skip      string
}

func (it Item) Empty() bool {
	return it.Kind == "" && !it.Sensitive && it.Skip == ""
}

// FromText validates and hashes a clipboard string.
func FromText(s string) (Item, error) {
	if s == "" {
		return Item{}, errors.New("空文本")
	}
	n := len([]byte(s))
	if n > MaxTextBytes {
		return Item{}, fmt.Errorf("文本 %d 字节，超过 %d", n, MaxTextBytes)
	}
	return Item{Kind: KindText, Text: s, Hash: HashText(s), Size: n}, nil
}

// FromPNG validates a PNG, checks the pixel budget, and hashes decoded pixels.
// The original PNG bytes are kept so the other side can paste the same file.
func FromPNG(data []byte) (Item, error) {
	if len(data) == 0 {
		return Item{}, errors.New("空图片")
	}
	if len(data) > MaxImageBytes {
		return Item{}, fmt.Errorf("图片 %d 字节，超过 %d", len(data), MaxImageBytes)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Item{}, fmt.Errorf("不是 PNG: %w", err)
	}
	if err := checkPixels(cfg.Width, cfg.Height); err != nil {
		return Item{}, err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return Item{}, fmt.Errorf("解码 PNG: %w", err)
	}
	return Item{
		Kind:   KindImage,
		PNG:    data,
		Hash:   HashImage(img),
		Width:  cfg.Width,
		Height: cfg.Height,
		Size:   len(data),
	}, nil
}

func checkPixels(w, h int) error {
	if w <= 0 || h <= 0 || w > 20000 || h > 20000 {
		return fmt.Errorf("图片尺寸 %dx%d 不支持", w, h)
	}
	if uint64(w)*uint64(h) > MaxPixels {
		return fmt.Errorf("图片 %dx%d 超过 %d 像素", w, h, MaxPixels)
	}
	return nil
}

func HashText(s string) [32]byte {
	return blake3.Sum256([]byte(s))
}

// HashImage hashes width, height, and non-premultiplied RGBA. PNG bytes are
// not hashed: two encoders can emit different files for the same pixels.
func HashImage(img image.Image) [32]byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	hsh := blake3.New()
	var dim [8]byte
	binary.LittleEndian.PutUint32(dim[0:4], uint32(w))
	binary.LittleEndian.PutUint32(dim[4:8], uint32(h))
	_, _ = hsh.Write(dim[:])
	_, _ = hsh.Write(dst.Pix)
	var out [32]byte
	copy(out[:], hsh.Sum(nil))
	return out
}
