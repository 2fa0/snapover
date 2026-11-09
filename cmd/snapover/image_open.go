package main

import "time"

const imageOpenTimeout = 10 * time.Second

// openSystemImage hands one staged PNG to the system viewer.
// Tests replace it so opening an image does not launch Preview.
var openSystemImage = openSystemImageWithOS

// previewFlyoutSize is the hover card in DIPs. A short work area shrinks the
// card; the picture then fits that card instead of scrolling.
func previewFlyoutSize(contentW, contentH, workW, workH int) (int, int) {
	w := max(220, contentW+24)
	h := contentH + 64
	if workW > 32 {
		w = min(w, workW-16)
	}
	if workH > 32 {
		h = min(h, workH-16)
	}
	return max(160, w), max(80, h)
}
