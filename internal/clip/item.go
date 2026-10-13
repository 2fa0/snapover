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
	"strings"

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
// Hash is blake3 of LF-normalized text, or of width||height||NRGBA pixels.
type Item struct {
	Kind Kind
	Text string
	PNG  []byte
	// Thumbnail holds only a small PNG, never the decoded original pixels.
	Thumbnail []byte
	Hash      [32]byte
	Width     int
	Height    int
	Size      int
	Sensitive bool
	Skip      string
	// SourceApp is the foreground app that owned this copy. Empty when unknown.
	// It is not part of the content hash.
	SourceApp string
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
	s = normalizeNewlines(s)
	n = len(s)
	return Item{Kind: KindText, Text: s, Hash: HashText(s), Size: n}, nil
}

// FromPNG validates a PNG, checks the pixel budget, and hashes decoded pixels.
// The original PNG bytes are kept so the other side can paste the same file.
func FromPNG(data []byte) (Item, error) {
	img, err := DecodePNG(data)
	if err != nil {
		return Item{}, err
	}
	b := img.Bounds()
	thumb, _ := ThumbnailPNG(img, 160)
	item := Item{
		Kind:      KindImage,
		PNG:       data,
		Thumbnail: thumb,
		Hash:      HashImage(img),
		Width:     b.Dx(),
		Height:    b.Dy(),
		Size:      len(data),
	}
	ReclaimImageMemory(item.Width, item.Height)
	return item, nil
}

// PNGForPaste validates a saved image without building synchronization metadata.
func PNGForPaste(data []byte) (Item, error) {
	img, err := DecodePNG(data)
	if err != nil {
		return Item{}, err
	}
	b := img.Bounds()
	ReclaimImageMemory(b.Dx(), b.Dy())
	return Item{Kind: KindImage, PNG: data, Width: b.Dx(), Height: b.Dy(), Size: len(data)}, nil
}

// DecodePNG checks the clipboard image limits without computing a content hash.
func DecodePNG(data []byte) (image.Image, error) {
	if len(data) == 0 {
		return nil, errors.New("空图片")
	}
	if len(data) > MaxImageBytes {
		return nil, fmt.Errorf("图片 %d 字节，超过 %d", len(data), MaxImageBytes)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("不是 PNG: %w", err)
	}
	if err := checkPixels(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("解码 PNG: %w", err)
	}
	return img, nil
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
	return blake3.Sum256([]byte(normalizeNewlines(s)))
}

// HashImage hashes width, height, and non-premultiplied RGBA. PNG bytes are
// not hashed: two encoders can emit different files for the same pixels.
func HashImage(img image.Image) [32]byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	hsh := blake3.New()
	var dim [8]byte
	binary.LittleEndian.PutUint32(dim[0:4], uint32(w))
	binary.LittleEndian.PutUint32(dim[4:8], uint32(h))
	_, _ = hsh.Write(dim[:])
	if src, ok := img.(*image.NRGBA); ok {
		for y := b.Min.Y; y < b.Max.Y; y++ {
			i := src.PixOffset(b.Min.X, y)
			_, _ = hsh.Write(src.Pix[i : i+4*w])
		}
	} else {
		// Keep conversion memory proportional to the width, not the pixel count.
		row := image.NewNRGBA(image.Rect(0, 0, w, 1))
		for y := b.Min.Y; y < b.Max.Y; y++ {
			draw.Draw(row, row.Bounds(), img, image.Pt(b.Min.X, y), draw.Src)
			_, _ = hsh.Write(row.Pix)
		}
	}
	var out [32]byte
	copy(out[:], hsh.Sum(nil))
	return out
}

// CleanSource keeps a short display name safe to store and send.
// Newlines and control characters are removed. An empty result means unknown.
func CleanSource(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		switch r {
		case '\n', '\r', '\t':
			r = ' '
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		n++
		if n >= 64 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}
