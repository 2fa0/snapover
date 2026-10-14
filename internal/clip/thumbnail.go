package clip

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"math"

	"golang.org/x/image/draw"
)

// Preview dimensions support a 560x340 DIP flyout at 2x without retaining
// original pixels. RGBA pixels plus smaller mip levels stay below 4 MiB.
const PreviewWidth, PreviewHeight = 1120, 680

func PreviewPNG(img image.Image) ([]byte, error) {
	b := img.Bounds()
	if b.Dx() < 1 || b.Dy() < 1 {
		return nil, errors.New("空图片")
	}
	scale := min(1.0, float64(PreviewWidth)/float64(b.Dx()), float64(PreviewHeight)/float64(b.Dy()))
	w, h := max(1, int(math.Round(float64(b.Dx())*scale))), max(1, int(math.Round(float64(b.Dy())*scale)))
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ThumbnailPNG scales into a bounded destination without copying the original.
func ThumbnailPNG(img image.Image, maxSide int) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 || maxSide < 1 {
		return nil, errors.New("空图片或无效缩略图尺寸")
	}
	tw, th := w, h
	if w > maxSide || h > maxSide {
		if w >= h {
			tw, th = maxSide, max(1, h*maxSide/w)
		} else {
			tw, th = max(1, w*maxSide/h), maxSide
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, tw, th))
	draw.NearestNeighbor.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
