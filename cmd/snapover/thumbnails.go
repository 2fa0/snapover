package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hashicorp/golang-lru/v2/simplelru"
)

const (
	maxThumbCache   = 128
	maxThumbBytes   = 8 << 20
	maxThumbPending = 32
	thumbWorkers    = 2
	thumbIdle       = time.Minute
)

type thumbRequest struct{ id string }
type thumbResult struct {
	request *thumbRequest
	bitmap  *ui.Bitmap
}

// Include the smaller mip levels MyGo creates while displaying a bitmap.
func thumbMemory(bmp *ui.Bitmap) int {
	if bmp == nil {
		return 0
	}
	w, h := bmp.Size()
	return w * h * 4 * 4 / 3
}

func (a *app) initThumbnails() {
	if a.thumbs != nil {
		return
	}
	a.thumbs, _ = simplelru.NewLRU[string, *ui.Bitmap](maxThumbCache, func(_ string, bmp *ui.Bitmap) {
		a.thumbBytes -= thumbMemory(bmp)
	})
	a.thumbPending = make(map[string]*thumbRequest)
	a.thumbResults = make(chan thumbResult, thumbWorkers)
}

func (a *app) thumb(id string) *ui.Bitmap {
	a.initThumbnails()
	a.touchThumbnails()
	if bmp, ok := a.thumbs.Get(id); ok {
		return bmp
	}
	if a.thumbPending[id] == nil && len(a.thumbPending) < maxThumbPending {
		r := &thumbRequest{id}
		a.thumbPending[id] = r
		a.thumbQueue = append(a.thumbQueue, r)
		a.dispatchThumbnails()
	}
	return nil
}

func (a *app) dispatchThumbnails() {
	for a.thumbActive < thumbWorkers && len(a.thumbQueue) > 0 {
		r := a.thumbQueue[0]
		a.thumbQueue[0] = nil
		a.thumbQueue = a.thumbQueue[1:]
		if a.thumbPending[r.id] != r {
			continue
		}
		a.thumbActive++
		store, results, host := a.hist, a.thumbResults, a.win
		go func() {
			// Both the disk read and bitmap conversion happen outside the view.
			raw, err := store.Thumb(r.id)
			var bmp *ui.Bitmap
			if err == nil {
				bmp, _ = ui.DecodeBitmap(raw)
			}
			results <- thumbResult{r, bmp}
			if host != nil {
				host.Update(a.drainThumbnails)
			}
		}()
	}
}

func (a *app) drainThumbnails() {
	changed := false
	for {
		select {
		case result := <-a.thumbResults:
			changed = true
			a.thumbActive--
			r := result.request
			if a.thumbPending[r.id] == r {
				delete(a.thumbPending, r.id)
				a.thumbBytes += thumbMemory(result.bitmap)
				a.thumbs.Add(r.id, result.bitmap)
				for a.thumbBytes > maxThumbBytes {
					a.thumbs.RemoveOldest()
				}
			}
		default:
			a.dispatchThumbnails()
			if changed && a.picker != nil {
				a.picker.Invalidate()
			}
			return
		}
	}
}

func (a *app) touchThumbnails() {
	a.thumbUsedAt = time.Now()
	if a.thumbTimer == nil && a.win != nil {
		a.scheduleThumbnailTrim(thumbIdle)
	}
}

func (a *app) scheduleThumbnailTrim(after time.Duration) {
	host := a.win
	a.thumbTimer = time.AfterFunc(after, func() {
		host.Update(func() {
			a.thumbTimer = nil
			if a.root != nil && a.root.Err() != nil {
				return
			}
			if left := thumbIdle - time.Since(a.thumbUsedAt); left > 0 {
				a.scheduleThumbnailTrim(left)
				return
			}
			if a.picker != nil || (a.page == pageHistory && a.win.IsVisible()) {
				a.scheduleThumbnailTrim(thumbIdle)
				return
			}
			a.releaseThumbnails()
		})
	})
}

func (a *app) releaseThumbnails() {
	if a.thumbs != nil {
		a.thumbs.Purge()
	}
	clear(a.thumbPending)
	a.thumbQueue = nil
}
