package main

import (
	"image"
	"image/draw"
)

// imageForDisplay consumes an exclusively owned decoded image. MyGo needs
// premultiplied RGBA; convert NRGBA in place to avoid another full-size buffer.
func imageForDisplay(img image.Image) image.Image {
	if src, ok := img.(*image.NRGBA); ok {
		dst := &image.RGBA{Pix: src.Pix, Stride: src.Stride, Rect: src.Rect}
		draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
		return dst
	}
	return img
}
