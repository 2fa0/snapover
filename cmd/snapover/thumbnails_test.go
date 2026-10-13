package main

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
)

func TestThumbnailsLoadAsynchronouslyAndReleaseIdleCache(t *testing.T) {
	a := interfaceTestApp(t)
	item, _ := clip.FromText("no thumbnail")
	entry, err := a.hist.Add("local", "", item)
	if err != nil {
		t.Fatal(err)
	}
	if a.thumb(entry.ID) != nil || a.thumbs.Contains(entry.ID) {
		t.Fatal("thumbnail request should not perform disk work in the view")
	}
	waitThumbnails(t, a)
	if !a.thumbs.Contains(entry.ID) || len(a.thumbPending) != 0 {
		t.Fatal("missing thumbnails should be cached after completion")
	}
	a.releaseThumbnails()
	if a.thumbs.Len() != 0 || a.thumbBytes != 0 {
		t.Fatal("idle release retained the cache")
	}
}

func TestThumbnailWorkerDecodesStoredImage(t *testing.T) {
	a := interfaceTestApp(t)
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 320, 240))); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromPNG(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := a.hist.Add("local", "", item)
	if err != nil {
		t.Fatal(err)
	}
	if a.thumb(entry.ID) != nil {
		t.Fatal("first frame should show a placeholder")
	}
	waitThumbnails(t, a)
	bmp := a.thumb(entry.ID)
	if bmp == nil {
		t.Fatal("completed thumbnail was not available")
	}
	if w, h := bmp.Size(); w != 160 || h != 120 || a.thumbBytes != thumbMemory(bmp) {
		t.Fatalf("unexpected cached thumbnail: %dx%d, bytes=%d", w, h, a.thumbBytes)
	}
}

func TestThumbnailsBoundMemoryAndRejectLateResults(t *testing.T) {
	a := interfaceTestApp(t)
	a.initThumbnails()
	bmp := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 160, 160)))
	for i := range 100 {
		r := &thumbRequest{id: string(rune(i + 100))}
		a.thumbPending[r.id] = r
		a.thumbActive++
		a.thumbResults <- thumbResult{r, bmp}
		a.drainThumbnails()
	}
	if a.thumbBytes > maxThumbBytes || a.thumbs.Len() >= maxThumbCache {
		t.Fatal("cache should enforce the byte budget before its item limit")
	}
	r := &thumbRequest{"late"}
	a.thumbPending[r.id] = r
	a.thumbActive++
	a.releaseThumbnails()
	a.thumbResults <- thumbResult{r, bmp}
	a.drainThumbnails()
	if a.thumbs.Len() != 0 || a.thumbBytes != 0 || a.thumbActive != 0 {
		t.Fatal("late load repopulated a released cache")
	}
}

func TestThumbnailsBoundQueueAndDropDeletedRecords(t *testing.T) {
	a := interfaceTestApp(t)
	for i := range 100 {
		a.thumb(string(rune(i + 100)))
	}
	if a.thumbActive > thumbWorkers || len(a.thumbPending) > maxThumbPending {
		t.Fatal("thumbnail requests exceeded concurrency or queue limits")
	}
	a.refreshEntries()
	waitThumbnails(t, a)
	if a.thumbs.Len() != 0 {
		t.Fatal("deleted entries returned to the cache")
	}
}
