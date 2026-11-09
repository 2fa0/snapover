package main

import (
	"fmt"
	"math"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
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
	parent *mygo.Window
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

func (a *app) previewParentActive(parent *mygo.Window) bool {
	return parent != nil && !a.quitting && (parent == a.picker || (parent == a.win && a.page == pageHistory))
}

func (a *app) finishPreviewLoad() {
	a.pickLoading = false
	for _, win := range []*mygo.Window{a.win, a.picker, a.pickPreview.win} {
		if win != nil {
			win.Invalidate()
		}
	}
}

func (a *app) updatePickPreview(c *ui.Context, parent *mygo.Window, id string, row ui.Rect) {
	if !a.previewParentActive(parent) {
		return
	}
	if a.pickPreview.parent != parent {
		if id == "" {
			return
		}
		a.closePickPreview()
		a.pickPreview.parent = parent
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
		s.anchor = mygo.Rectangle{X: int(row.X), Y: int(row.Y), Width: int(row.W), Height: int(row.H)}
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
		a.loadPickPreview(s.id, parent)
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

func (a *app) loadPickPreview(id string, parent *mygo.Window) {
	// Keep the limit on the app: closing a preview must not start another
	// load while its previous request is still running.
	if a.pickLoading || !a.previewParentActive(parent) {
		return
	}
	a.pickLoading = true
	request := &struct{ id string }{id}
	a.pickPreview.request = request
	a.pickPreview.parent = parent
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
		var files history.FilePreview
		var more bool
		var err error
		isText := entry.Kind == string(clip.KindText)
		isFiles := entry.Kind == string(clip.KindFiles)
		if isFiles {
			files, err = a.hist.FilePreview(id)
		} else if isText {
			text, more, err = a.hist.TextPreview(id)
		} else {
			bmp, err = previewBitmap(a.hist, id)
		}
		mygo.RunOnMain(func() {
			a.finishPreviewLoad()
			s := &a.pickPreview
			if !a.previewParentActive(parent) || s.parent != parent || s.request != request || s.hover.id != id {
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
			if isFiles {
				w, h = 396, 44
				if files.Directory || len(files.Items) > 0 {
					h = max(220, 48+min(len(files.Items), 12)*24)
				}
			}
			if err != nil {
				w, h = 240, 60
			}
			area := a.pickArea
			if parent != a.picker {
				area = windowWorkArea(parent)
			}
			winW, winH := previewFlyoutSize(w, h, area.Width, area.Height)
			var textPreview *editor.State
			if isText && err == nil {
				textPreview = newPickTextEditor(text, a.tr("文本预览"), pickTextLanguage(text))
			}
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
						if isFiles {
							a.pickFilePreview(c, files, err)
						} else if isText {
							a.pickTextPreview(c, textPreview, err)
						} else if bmp == nil {
							ui.Text(c, a.tr("图片预览加载失败，请重新悬停重试")).TextColor(t.TextMuted)
						} else {
							// Fit the whole picture. A long screenshot becomes a strip;
							// reading it is the system viewer's job, not this card's.
							ui.Image(c, bmp).Grow(1).Shrink(1).FillWidth().MaxHeight(float32(h)).Fit(ui.Contain)
						}
						if isFiles {
							ui.Text(c, formatSize(entry.Size)).FontSize(11).TextColor(t.TextMuted).Shrink(0)
							return
						}
						ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
							meta := fmt.Sprintf("%d × %d", entry.Width, entry.Height)
							label := a.tr("放大查看")
							if isText {
								label, meta = a.tr("查看全文"), formatSize(entry.Size)
								if more {
									meta = a.tr(meta + " · 还有更多内容")
								}
							}
							ui.Text(c, meta).FontSize(11).TextColor(t.TextMuted).Grow(1)
							if a.pickLoading {
								label = a.tr("正在加载…")
							}
							if ui.Button(c, label).Disabled(a.pickLoading).Clicked() {
								parent.Update(func() {
									if a.previewParentActive(parent) {
										if isText {
											a.loadPickText(id, parent)
										} else {
											// The window that opens can zoom the picture
											// itself, which the card cannot.
											a.finishPreview(parent)
											a.openImageViewer(entry)
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
				textPreview = nil
				files = history.FilePreview{}
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

func (a *app) finishPreview(parent *mygo.Window) {
	if parent == a.picker {
		a.closePicker()
	} else {
		a.closePickPreview()
	}
}

func (a *app) previewError(parent *mygo.Window, message string) {
	if parent == a.picker {
		a.pickFlash = message
	} else {
		a.flash = message
	}
	parent.Invalidate()
	if a.pickPreview.win != nil {
		a.pickPreview.win.Invalidate()
	}
}
