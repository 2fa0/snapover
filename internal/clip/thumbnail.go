package clip

import (
	"bytes"
	"errors"
	"image"
	"image/png"

	"golang.org/x/image/draw"
)

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
