package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
)

const (
	pickPreviewDelay      = 300 * time.Millisecond
	pickPreviewLeaveDelay = 180 * time.Millisecond
)

type previewHover struct {
	id    string
	since time.Time
	left  time.Time
}

// The grace period lets the pointer cross the gap into the preview.
func (h *previewHover) update(id string, inside bool, now time.Time) bool {
	if id != "" {
		if id != h.id || !h.left.IsZero() {
			h.id, h.since = id, now
		}
		h.left = time.Time{}
	} else if inside {
		h.left = time.Time{}
	} else if h.id != "" {
		if h.left.IsZero() {
			h.left = now
		}
		if now.Sub(h.left) >= pickPreviewLeaveDelay {
			*h = previewHover{}
		}
	}
	return h.id != "" && h.left.IsZero() && now.Sub(h.since) >= pickPreviewDelay
}

type pickerPreview struct {
	hover  previewHover
	inside bool
	anchor mygo.Rectangle
	win    *mygo.Window
	id     string
	// A request identity prevents a late load from reopening a dismissed preview.
	request *struct{ id string }
}

func (a *app) closePickPreview() {
	p := a.pickPreview.win
	a.pickPreview = pickerPreview{}
	if p != nil {
		p.Close()
	}
}

func (a *app) updatePickPreview(c *ui.Context, id string, row ui.Rect) {
	if a.picker == nil {
		return
	}
	s := &a.pickPreview
	if s.inside {
		id = ""
	}
	ready := s.hover.update(id, s.inside, c.Now())
	if s.hover.id == "" {
		a.closePickPreview()
		return
	}
	if id != "" {
		width, _ := c.Size()
		s.anchor = mygo.Rectangle{X: 0, Y: int(row.Y), Width: int(width), Height: int(row.H)}
	}
	if s.id != s.hover.id {
		if s.win != nil {
			s.win.Close()
			s.win = nil
			s.inside = false
		}
		s.id, s.request = s.hover.id, nil
	} else if s.win != nil && id != "" {
		s.win.SetAnchor(s.anchor)
	}
	if ready && s.win == nil && s.request == nil {
		a.loadPickPreview(s.id)
	}
	if delay := s.hover.nextWake(c.Now(), s.win != nil); delay > 0 {
		c.After(delay)
	}
}

func (h previewHover) nextWake(now time.Time, previewOpen bool) time.Duration {
	if h.id == "" {
		return 0
	}
	if !h.left.IsZero() {
		return max(0, pickPreviewLeaveDelay-now.Sub(h.left))
	}
	if !previewOpen {
		return max(0, pickPreviewDelay-now.Sub(h.since))
	}
	return 0
}

func previewImageSize(width, height int) (int, int) {
	if width <= 0 || height <= 0 {
		return 1, 1
	}
	scale := min(1.0, float64(clip.PreviewWidth/2)/float64(width), float64(clip.PreviewHeight/2)/float64(height))
	return max(1, int(math.Round(float64(width)*scale))), max(1, int(math.Round(float64(height)*scale)))
}

func (a *app) loadPickPreview(id string) {
	// Keep the limit on the app: closing a picker must not start another
	// load while its previous request is still running.
	if a.pickLoading {
		return
	}
	a.pickLoading = true
	request := &struct{ id string }{id}
	a.pickPreview.request = request
	parent := a.picker
	var entry history.Entry
	for _, candidate := range a.entries {
		if candidate.ID == id {
			entry = candidate
			break
		}
	}
	go func() {
		var bmp *ui.Bitmap
		var text string
		var more bool
		var err error
		isText := entry.Kind == string(clip.KindText)
		if isText {
			text, more, err = a.hist.TextPreview(id)
		} else {
			bmp, err = previewBitmap(a.hist, id)
		}
		mygo.RunOnMain(func() {
			a.pickLoading = false
			if a.picker != nil {
				a.picker.Invalidate()
			}
			s := &a.pickPreview
			if a.picker != parent || s.request != request || s.hover.id != id {
				return
			}
			if !s.hover.left.IsZero() {
				s.request = nil
				return
			}
			w, h := previewImageSize(entry.Width, entry.Height)
			if isText {
				w, h = 396, 300
			}
			if err != nil {
				w, h = 240, 60
			}
			workW, workH := previewWorkSize()
			winW, winH := previewFlyoutSize(w, h, workW, workH)
			var p *mygo.Window
			p = mygo.NewFlyout(mygo.FlyoutOptions{
				Parent: parent, Anchor: s.anchor, Placement: mygo.PlacementRightStart,
				Gap: 8, Width: winW, Height: winH,
				Hidden: true, Shadow: true,
				Content: ui.View(func(c *ui.Context) {
					t := c.Theme()
					box := ui.Column(c).Fill().Padding(12).Gap(8).Background(t.Surface)
					if inside := box.Hovered(); a.pickPreview.win == p && inside != a.pickPreview.inside {
						a.pickPreview.inside = inside
						parent.Invalidate()
					}
					box.Children(func() {
						if isText {
							pickTextPreview(c, text, err)
						} else if bmp == nil {
							ui.Text(c, "图片预览加载失败，请重新悬停重试").TextColor(t.TextMuted)
						} else {
							// Fit the whole picture. A long screenshot becomes a strip;
							// reading it is the system viewer's job, not this card's.
							ui.Image(c, bmp).Grow(1).Shrink(1).FillWidth().MaxHeight(float32(h)).Fit(ui.Contain)
						}
						ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
							meta := fmt.Sprintf("%d × %d", entry.Width, entry.Height)
							label := "查看原图"
							if isText {
								label, meta = "查看全文", formatSize(entry.Size)
								if more {
									meta += " · 还有更多内容"
								}
							}
							ui.Text(c, meta).FontSize(11).TextColor(t.TextMuted).Grow(1)
							if a.pickLoading {
								label = "正在加载…"
							}
							if ui.Button(c, label).Disabled(a.pickLoading).Clicked() {
								parent.Update(func() {
									if a.picker == parent {
										if isText {
											a.loadPickText(id, parent)
										} else {
											a.loadPickImage(id, parent)
										}
									}
								})
							}
						})
					})
				}),
			})
			s.win = p
			p.OnClosed(func() {
				// Native windows can outlive their content closures.
				bmp = nil
				text = ""
				if a.pickPreview.win == p {
					a.pickPreview.win = nil
					a.pickPreview.inside = false
					parent.Invalidate()
				}
			})
			p.ShowInactive()
		})
	}()
}

func previewBitmap(store *history.Store, id string) (*ui.Bitmap, error) {
	img, err := store.PreviewImage(id)
	if err != nil {
		return nil, err
	}
	return ui.NewBitmap(imageForDisplay(img)), nil
}

func (a *app) loadPickImage(id string, parent *mygo.Window) {
	if a.pickLoading || a.picker != parent || a.pickPreview.hover.id != id {
		return
	}
	a.pickLoading = true
	if a.pickPreview.win != nil {
		a.pickPreview.win.Invalidate()
	}
	go func() {
		path, err := a.hist.StageImage(id)
		mygo.RunOnMain(func() {
			// Opening is terminal for this image. A dismissed picker must not
			// launch the viewer afterwards.
			if err != nil || a.quitting || a.picker != parent {
				a.finishPickImage(parent, err)
				return
			}
			ctx := a.root
			if ctx == nil {
				ctx = context.Background()
			}
			ctx, cancel := context.WithTimeout(ctx, imageOpenTimeout)
			go func() {
				defer cancel()
				err := openSystemImage(ctx, path)
				mygo.RunOnMain(func() { a.finishPickImage(parent, err) })
			}()
		})
	}()
}

func (a *app) finishPickImage(parent *mygo.Window, err error) {
	a.pickLoading = false
	if a.picker != nil {
		a.picker.Invalidate()
	}
	if a.quitting || a.picker != parent {
		return
	}
	if err != nil {
		a.pickFlash = "原图打开失败，请重新选择图片后重试"
		if a.pickPreview.win != nil {
			a.pickPreview.win.Invalidate()
		}
		return
	}
	a.closePicker()
}
