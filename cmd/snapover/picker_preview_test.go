package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
)

func TestPreviewHoverDelayAndSwitch(t *testing.T) {
	var h previewHover
	now := time.Unix(100, 0)
	if h.update("first", false, now) || h.update("first", false, now.Add(299*time.Millisecond)) {
		t.Fatal("preview appeared before dwell delay")
	}
	if !h.update("first", false, now.Add(300*time.Millisecond)) {
		t.Fatal("preview did not appear after dwell delay")
	}
	if h.update("second", false, now.Add(310*time.Millisecond)) || h.id != "second" {
		t.Fatal("switching rows should restart dwell delay")
	}
	if !h.update("second", false, now.Add(610*time.Millisecond)) {
		t.Fatal("second row did not become ready")
	}
}

func TestPreviewHoverCrossGapAndDismiss(t *testing.T) {
	var h previewHover
	now := time.Unix(100, 0)
	h.update("image", false, now)
	h.update("image", false, now.Add(300*time.Millisecond))
	h.update("", false, now.Add(310*time.Millisecond))
	if h.id != "image" {
		t.Fatal("preview dismissed before pointer could cross gap")
	}
	if !h.update("", true, now.Add(450*time.Millisecond)) {
		t.Fatal("entering preview should keep it visible")
	}
	h.update("", true, now.Add(time.Second))
	h.update("", false, now.Add(1100*time.Millisecond))
	h.update("", false, now.Add(1280*time.Millisecond))
	if h.id != "" {
		t.Fatal("preview should dismiss after leaving both surfaces")
	}
}

func TestPreviewHoverRequiresContinuousDwell(t *testing.T) {
	var h previewHover
	now := time.Unix(100, 0)
	h.update("image", false, now)
	h.update("", false, now.Add(200*time.Millisecond))
	if h.update("image", false, now.Add(250*time.Millisecond)) || h.update("image", false, now.Add(300*time.Millisecond)) {
		t.Fatal("returning to a row should require a fresh dwell")
	}
	if !h.update("image", false, now.Add(550*time.Millisecond)) {
		t.Fatal("preview should appear after fresh dwell")
	}
}

func TestPreviewSchedulesOnlyDwellAndDismissalDeadlines(t *testing.T) {
	var h previewHover
	now := time.Unix(100, 0)
	if h.nextWake(now, false) != 0 {
		t.Fatal("idle picker should not wake periodically")
	}
	h.update("image", false, now)
	if got := h.nextWake(now.Add(100*time.Millisecond), false); got != 200*time.Millisecond {
		t.Fatalf("dwell wake = %v", got)
	}
	if h.nextWake(now.Add(time.Second), true) != 0 {
		t.Fatal("open preview should wait for pointer events")
	}
	h.update("", false, now.Add(time.Second))
	if got := h.nextWake(now.Add(1050*time.Millisecond), true); got != 130*time.Millisecond {
		t.Fatalf("dismissal wake = %v", got)
	}
	h.update("", true, now.Add(1100*time.Millisecond))
	if h.nextWake(now.Add(1200*time.Millisecond), true) != 0 {
		t.Fatal("entering preview should cancel the dismissal deadline")
	}
}

func TestPreviewImageSize(t *testing.T) {
	for _, tc := range []struct{ w, h, wantW, wantH int }{
		{2000, 1000, 456, 228},
		{1000, 10000, 31, 308},
		{80, 40, 80, 40},
		{0, 0, 1, 1},
	} {
		w, h := previewImageSize(tc.w, tc.h)
		if w != tc.wantW || h != tc.wantH {
			t.Fatalf("size(%d, %d) = %d × %d, want %d × %d", tc.w, tc.h, w, h, tc.wantW, tc.wantH)
		}
	}
}

func TestPreviewUsesThumbnailWithoutReadingOriginal(t *testing.T) {
	dir := t.TempDir()
	store, err := history.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 400, 1000))); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromPNG(data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := store.Add(history.DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "blobs", entry.ImageFile)); err != nil {
		t.Fatal(err)
	}
	bmp, err := previewBitmap(store, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	w, h := bmp.Size()
	if w != 64 || h != 160 {
		t.Fatalf("preview decoded full image instead of thumbnail: %dx%d", w, h)
	}
	if _, err := previewBitmap(store, "missing"); !os.IsNotExist(err) {
		t.Fatalf("missing preview: %v", err)
	}
}
