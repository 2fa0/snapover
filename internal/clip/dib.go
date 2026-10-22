package clip

import (
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
)

// DIB pixels are untrusted. Validate the complete layout and size before
// allocating; common clipboard DIBs are uncompressed 24/32-bit BGR(A).
func decodeDIB(raw []byte) (*image.NRGBA, error) {
	invalid := errors.New("不支持或损坏的剪贴板位图")
	if len(raw) < 40 {
		return nil, invalid
	}
	le := binary.LittleEndian
	header := uint64(le.Uint32(raw))
	width, signedHeight := int(int32(le.Uint32(raw[4:]))), int64(int32(le.Uint32(raw[8:])))
	height := signedHeight
	if height < 0 {
		height = -height
	}
	if err := checkPixels(width, int(height)); err != nil {
		return nil, err
	}
	bits, compression := le.Uint16(raw[14:]), le.Uint32(raw[16:])
	if le.Uint16(raw[12:]) != 1 || (bits != 24 && bits != 32) || (compression != 0 && compression != 3) || header < 40 || header > uint64(len(raw)) {
		return nil, invalid
	}
	offset := header
	if compression == 3 && header == 40 {
		offset += 12
	}
	if compression == 3 {
		if bits != 32 || len(raw) < 52 || le.Uint32(raw[40:]) != 0xff0000 || le.Uint32(raw[44:]) != 0xff00 || le.Uint32(raw[48:]) != 0xff {
			return nil, invalid
		}
	}
	stride := ((uint64(width)*uint64(bits) + 31) / 32) * 4
	if offset > uint64(len(raw)) || uint64(height) > (uint64(len(raw))-offset)/stride {
		return nil, invalid
	}
	out := image.NewNRGBA(image.Rect(0, 0, width, int(height)))
	px := int(bits / 8)
	alpha := bits == 32 && header >= 56 && le.Uint32(raw[52:]) == 0xff000000
	for y := 0; y < int(height); y++ {
		row := y
		if signedHeight > 0 {
			row = int(height) - 1 - y
		}
		src := raw[int(offset)+row*int(stride):]
		dst := out.Pix[y*out.Stride:]
		for x := 0; x < width; x++ {
			i, j := x*px, x*4
			a := byte(255)
			if alpha {
				a = src[i+3]
			}
			dst[j], dst[j+1], dst[j+2], dst[j+3] = src[i+2], src[i+1], src[i], a
		}
	}
	return out, nil
}

// Keep the established CF_DIB representation for applications that do not
// accept PNG. A row buffer avoids per-pixel interface allocations.
func encodeDIBInto(raw []byte, img image.Image) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	// Native GlobalAlloc memory is not zeroed; initialize BI_RGB and the
	// unused resolution/palette fields before filling the remaining header.
	clear(raw[:40])
	le := binary.LittleEndian
	le.PutUint32(raw, 40)
	le.PutUint32(raw[4:], uint32(w))
	le.PutUint32(raw[8:], uint32(-int32(h)))
	le.PutUint16(raw[12:], 1)
	le.PutUint16(raw[14:], 32)
	le.PutUint32(raw[20:], uint32(4*w*h))
	row := image.NewNRGBA(image.Rect(0, 0, w, 1))
	for y := 0; y < h; y++ {
		draw.Draw(row, row.Bounds(), img, image.Pt(b.Min.X, b.Min.Y+y), draw.Src)
		dst := raw[40+y*w*4:]
		for x := 0; x < w; x++ {
			i := x * 4
			dst[i], dst[i+1], dst[i+2], dst[i+3] = row.Pix[i+2], row.Pix[i+1], row.Pix[i], row.Pix[i+3]
		}
	}
}
