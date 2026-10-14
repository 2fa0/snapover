package main

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
)

func pickTextPreview(c *ui.Context, text string, err error) {
	if err != nil {
		ui.Text(c, "文本预览加载失败，请重新悬停重试").TextColor(c.Theme().TextMuted)
		return
	}
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Text(c, text).FontSize(14).FillWidth()
	})
}

// rowNeedsPreview keeps hover previews for images and text the list cannot
// show whole. A short, uncut title is already the item.
func rowNeedsPreview(c *ui.Context, entry history.Entry, shown string, title ui.Element) bool {
	switch entry.Kind {
	case string(clip.KindImage):
		return true
	case string(clip.KindText):
		return textRowTruncated(c, entry, shown, title)
	default:
		return false
	}
}

func textRowTruncated(c *ui.Context, entry history.Entry, shown string, title ui.Element) bool {
	if history.TitleTruncated(entry) {
		return true
	}
	box := title.Bounds()
	if box.W <= 1 {
		return false
	}
	width, _ := c.MeasureText(0, ui.Span{Text: shown})
	return width > box.W+0.5
}

func (a *app) loadPickText(id string, parent *mygo.Window) {
	if a.pickLoading || a.picker != parent || a.pickPreview.hover.id != id {
		return
	}
	a.pickLoading = true
	if a.pickPreview.win != nil {
		a.pickPreview.win.Invalidate()
	}
	go func() {
		item, err := a.hist.PasteItem(id)
		mygo.RunOnMain(func() {
			a.pickLoading = false
			if a.picker != parent {
				return
			}
			parent.Invalidate()
			if err != nil {
				a.pickFlash = "全文加载失败，请重新选择文本后重试"
				if a.pickPreview.win != nil {
					a.pickPreview.win.Invalidate()
				}
				return
			}
			a.openPickText(item.Text)
		})
	}()
}

type pickTextReader struct {
	text     string
	started  bool
	pages    []string
	page     int
	pageText string
	prepared bool
}

func (r *pickTextReader) view(c *ui.Context) {
	if !r.prepared {
		if hasLongTextLine(r.text) {
			r.pages = textReaderPages(r.text)
			r.pageText = r.pages[0]
		}
		r.prepared = true
	}
	ui.Column(c).Fill().Gap(8).Children(func() {
		text := &r.text
		if len(r.pages) > 0 {
			text = &r.pageText
		}
		area := ui.TextAreaBase(c.Key("full-text").Key(r.page), text).Label("文本全文").ReadOnly(true).FontSize(14).Fill().AutoFocus()
		if !r.started {
			area.SetTextSelection(0, 0)
			r.started = true
		}
		if len(r.pages) > 0 {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "上一段").Disabled(r.page == 0).Clicked() {
					r.setPage(c, r.page-1)
				}
				ui.Textf(c, "第 %d / %d 段", r.page+1, len(r.pages)).Grow(1)
				if ui.Button(c, "下一段").Disabled(r.page == len(r.pages)-1).Clicked() {
					r.setPage(c, r.page+1)
				}
				if ui.Button(c, "复制全文").Clicked() {
					c.WriteClipboard(r.text)
				}
			})
			ui.Text(c, "文本包含超长行，分段显示；全选复制当前段，复制全文保留完整内容。").FontSize(11).TextColor(c.Theme().TextMuted)
		}
	})
}

const textReaderPageBytes = 16 << 10

func hasLongTextLine(text string) bool {
	for {
		line, rest, more := strings.Cut(text, "\n")
		if len(line) > textReaderPageBytes {
			return true
		}
		if !more {
			return false
		}
		text = rest
	}
}

// Keep each layout bounded without inserting breaks into the copied text.
func textReaderPages(text string) []string {
	var pages []string
	for len(text) > textReaderPageBytes {
		end := textReaderPageBytes
		for !utf8.RuneStart(text[end]) {
			end--
		}
		pages = append(pages, text[:end])
		text = text[end:]
	}
	return append(pages, text)
}

func (r *pickTextReader) setPage(c *ui.Context, page int) {
	r.page, r.pageText, r.started = page, r.pages[page], false
	c.Invalidate()
}

func (a *app) openPickText(text string) {
	if a.pickOriginal != nil {
		a.pickOriginal.Close()
	}
	reader := &pickTextReader{text: text}
	var viewer *mygo.Window
	viewer = mygo.NewWindow(mygo.WindowOptions{
		Title: "文本全文", Width: 720, Height: 560,
		Hidden: true, UseContentSize: true,
		Content: ui.View(func(c *ui.Context) {
			if c.Shortcut(0, ui.KeyEscape) {
				viewer.Update(viewer.Close)
			}
			t := c.Theme()
			ui.Column(c).Fill().Padding(16).Gap(12).Background(t.Surface).Children(func() {
				reader.view(c)
				ui.Text(c, "可选择文字复制 · Esc 关闭").FontSize(11).TextColor(t.TextMuted)
			})
		}),
	})
	a.pickOriginal = viewer
	viewer.OnClosed(func() {
		*reader = pickTextReader{}
		if a.pickOriginal == viewer {
			a.pickOriginal = nil
		}
	})
	a.closePicker()
	viewer.Show()
	viewer.Focus()
}
