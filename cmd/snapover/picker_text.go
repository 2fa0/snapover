package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func newPickTextPreview(text string) *editor.State {
	return newPickTextEditor(text, "文本预览", pickTextLanguage(text))
}

func newPickTextEditor(text, label string, language *editor.Language) *editor.State {
	buffer := editor.NewBuffer(text)
	buffer.SetLanguage(language)
	return &editor.State{
		Buffer:          buffer,
		Label:           label,
		ReadOnly:        true,
		SoftWrap:        true,
		LineNumbers:     editor.LineNumbersOn,
		NoIndentGuides:  true,
		NoFolding:       true,
		NoActiveLine:    true,
		NoScrollPastEnd: true,
	}
}

func pickTextPreview(c *ui.Context, state *editor.State, err error) {
	if err != nil || state == nil {
		ui.Text(c, "文本预览加载失败，请重新悬停重试").TextColor(c.Theme().TextMuted)
		return
	}
	editor.View(c.Key("text-preview"), state).Grow(1).FillWidth()
}

func (a *app) pickTextPreview(c *ui.Context, state *editor.State, err error) {
	if err != nil || state == nil {
		ui.Text(c, a.tr("文本预览加载失败，请重新悬停重试")).TextColor(c.Theme().TextMuted)
		return
	}
	editor.View(c.Key("text-preview"), state).Grow(1).FillWidth()
}

// rowNeedsPreview keeps hover previews for images and text the list cannot
// show whole. A short, uncut title is already the item.
func rowNeedsPreview(c *ui.Context, entry history.Entry, shown string, title ui.Element) bool {
	switch entry.Kind {
	case string(clip.KindImage):
		return true
	case string(clip.KindFiles):
		return entry.Folder || entry.FileCount > 1 || textRowTruncated(c, entry, shown, title)
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
	if a.pickLoading || !a.previewParentActive(parent) || a.pickPreview.hover.id != id {
		return
	}
	a.pickLoading = true
	request := a.pickPreview.request
	if a.pickPreview.win != nil {
		a.pickPreview.win.Invalidate()
	}
	go func() {
		item, err := a.hist.PasteItem(id)
		mygo.RunOnMain(func() {
			a.finishPreviewLoad()
			if !a.previewParentActive(parent) || a.pickPreview.request != request {
				return
			}
			if err != nil {
				a.previewError(parent, "全文加载失败，请重新选择文本后重试")
				return
			}
			a.finishPreview(parent)
			a.openPickText(item.Text)
		})
	}()
}

type pickTextReader struct {
	app      *app
	text     string
	pages    []string
	page     int
	pageText string
	prepared bool
	language *editor.Language
	editor   *editor.State
}

func (r *pickTextReader) tr(s string) string {
	if r != nil && r.app != nil {
		return r.app.tr(s)
	}
	return s
}

func (r *pickTextReader) view(c *ui.Context) {
	if !r.prepared {
		if len(r.text) > textReaderMaxEditorBytes || hasLongTextLine(r.text) {
			r.pages = textReaderPages(r.text)
			r.pageText = r.pages[0]
		}
		r.language = pickTextLanguage(r.text)
		r.prepared = true
	}
	if r.editor == nil {
		text := r.text
		if len(r.pages) > 0 {
			text = r.pageText
		}
		r.editor = newPickTextEditor(text, r.tr("文本全文"), r.language)
	}
	ui.Column(c).Fill().Gap(8).Children(func() {
		editor.View(c.Key("full-text").Key(r.page), r.editor).Fill().AutoFocus()
		if len(r.pages) > 0 {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, r.tr("上一段")).Disabled(r.page == 0).Clicked() {
					r.setPage(c, r.page-1)
				}
				ui.Text(c, r.tr(fmt.Sprintf("第 %d / %d 段", r.page+1, len(r.pages)))).Grow(1)
				if ui.Button(c, r.tr("下一段")).Disabled(r.page == len(r.pages)-1).Clicked() {
					r.setPage(c, r.page+1)
				}
				if ui.Button(c, r.tr("复制全文")).Clicked() {
					c.WriteClipboard(r.text)
				}
			})
		}
	})
}

const (
	textReaderPageBytes      = 16 << 10
	textReaderMaxEditorBytes = 256 << 10
)

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
	r.page, r.pageText, r.editor = page, r.pages[page], nil
	c.Invalidate()
}

func (a *app) openPickText(text string) {
	if a.pickOriginal != nil {
		a.pickOriginal.Close()
	}
	reader := &pickTextReader{app: a, text: text}
	var viewer *mygo.Window
	viewer = mygo.NewWindow(a.placeOnWorkingDisplay(mygo.WindowOptions{
		Title: a.tr("文本全文"), Width: 720, Height: 560,
		Hidden: true, UseContentSize: true,
		Content: appView(func(c *ui.Context) {
			if c.Shortcut(0, ui.KeyEscape) {
				viewer.Update(viewer.Close)
			}
			t := c.Theme()
			ui.Column(c).Fill().Padding(16).Gap(12).Background(t.Surface).Children(func() {
				reader.view(c)
			})
		}),
	}))
	// An empty menu overrides the application menu; nil inherits it on Windows.
	viewer.SetMenu(mygo.NewMenu(nil))
	a.pickOriginal = viewer
	viewer.OnClosed(func() {
		*reader = pickTextReader{}
		if a.pickOriginal == viewer {
			a.pickOriginal = nil
		}
	})
	viewer.Show()
	viewer.Focus()
}
