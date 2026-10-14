package main

import (
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
			var p *mygo.Window
			p = mygo.NewFlyout(mygo.FlyoutOptions{
				Parent: parent, Anchor: s.anchor, Placement: mygo.PlacementRightStart,
				Gap: 8, Width: max(220, w+24), Height: h + 64,
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
							ui.Image(c, bmp).Size(float32(w), float32(h)).Fit(ui.Contain)
						}
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
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
	// Keep at most one original window; release the previous pixels before
	// decoding another large image.
	if a.pickOriginal != nil {
		a.pickOriginal.Close()
	}
	if a.pickPreview.win != nil {
		a.pickPreview.win.Invalidate()
	}
	go func() {
		img, err := a.hist.Image(id)
		var bmp *ui.Bitmap
		if err == nil {
			bmp = ui.NewBitmap(imageForDisplay(img))
		}
		mygo.RunOnMain(func() {
			a.pickLoading = false
			if a.picker != nil {
				a.picker.Invalidate()
			}
			if a.picker != parent {
				if bmp != nil {
					w, h := bmp.Size()
					bmp = nil
					clip.ReclaimImageMemory(w, h)
				}
				return
			}
			if err != nil {
				a.pickFlash = "原图加载失败，请重新选择图片后重试"
				parent.Invalidate()
				if a.pickPreview.win != nil {
					a.pickPreview.win.Invalidate()
				}
				return
			}
			a.openPickImage(bmp)
		})
	}()
}

func (a *app) openPickImage(bmp *ui.Bitmap) {
	w, h := bmp.Size()
	var viewer *mygo.Window
	viewer = mygo.NewWindow(mygo.WindowOptions{
		Title: "图片原图", Width: min(960, max(360, w+24)), Height: min(720, max(240, h+24)),
		Hidden: true, UseContentSize: true,
		Content: ui.View(func(c *ui.Context) {
			if c.Shortcut(0, ui.KeyEscape) {
				viewer.Update(viewer.Close)
			}
			ui.ScrollBoth(c).Fill().Children(func() {
				ui.Image(c, bmp).Size(float32(w), float32(h)).Fit(ui.Contain)
			})
		}),
	})
	a.pickOriginal = viewer
	viewer.OnClosed(func() {
		// Closed native windows may outlive their content closures.
		bmp = nil
		clip.ReclaimImageMemory(w, h)
		if a.pickOriginal == viewer {
			a.pickOriginal = nil
		}
	})
	a.closePicker()
	viewer.Show()
	viewer.Focus()
}
