package main

import (
	"bytes"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestPickTextReaderCanCopyFullBodyWithoutEditing(t *testing.T) {
	body := "  开头\n\t缩进\n" + strings.Repeat("长文本内容\n", 2000) + "尾部完整内容  "
	r := &pickTextReader{text: body}
	tt := ui.NewTester(r.view, 720, 480)
	tt.Key(ui.Shift, ui.KeyRight)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); got != " " {
		t.Fatalf("reader should open at the beginning, copied %q", got)
	}
	tt.Type("不应写入")
	tt.Key(0, ui.KeyBackspace)
	if r.text != body {
		t.Fatal("read-only viewer changed the original text")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); got != body {
		t.Fatal("full text reader copied a truncated or changed body")
	}
}

func TestPickTextReaderLargeSingleLineStaysBoundedAndCopiesExactly(t *testing.T) {
	for _, body := range []string{
		strings.Repeat("A", clip.MaxTextBytes),
		strings.Repeat("中😀\t ", clip.MaxTextBytes/9),
	} {
		r := &pickTextReader{text: body}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		tt := ui.NewTester(r.view, 720, 480)
		runtime.ReadMemStats(&after)
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("opened %d bytes in %s, allocated %.1f MiB, heap delta %.1f MiB", len(body), time.Since(start), float64(allocated)/(1<<20), (float64(after.HeapAlloc)-float64(before.HeapAlloc))/(1<<20))
		if allocated > 32<<20 {
			t.Fatalf("opening a long text record allocated %d bytes", allocated)
		}
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		if tt.Clipboard() != r.pages[0] {
			t.Fatal("copying the current segment changed its content")
		}
		clickInterface(t, tt, "复制全文")
		if tt.Clipboard() != body {
			t.Fatal("copying full text changed or truncated its content")
		}
		clickInterface(t, tt, "下一段")
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		if r.page != 1 || tt.Clipboard() != r.pages[1] {
			t.Fatal("next segment did not display and copy its original content")
		}
		clickInterface(t, tt, "上一段")
		if r.page != 0 || r.pageText != r.pages[0] {
			t.Fatal("previous segment did not restore the beginning")
		}
		tt.Type("不应写入")
		tt.Key(0, ui.KeyBackspace)
		if r.text != body || r.pageText != r.pages[0] {
			t.Fatal("long text viewer was editable")
		}
	}
}

func TestTextReaderPagesPreserveUTF8AndAllWhitespace(t *testing.T) {
	body := "  \t开头\n" + strings.Repeat("中😀 \t\n", 10000) + "\n尾部  "
	pages := textReaderPages(body)
	for _, page := range pages {
		if len(page) > textReaderPageBytes || !utf8.ValidString(page) {
			t.Fatal("segment exceeds the layout limit or splits a UTF-8 character")
		}
	}
	if strings.Join(pages, "") != body {
		t.Fatal("segmenting changed text or whitespace")
	}
}

func TestPickTextReaderLargeMultilineStaysBounded(t *testing.T) {
	prefix, line := "package main\n", "// 多行代码\n"
	body := prefix + strings.Repeat(line, (clip.MaxTextBytes-len(prefix))/len(line))
	r := &pickTextReader{text: body}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	tt := ui.NewTester(r.view, 720, 480)
	runtime.ReadMemStats(&after)
	t.Logf("opened %d bytes, allocated %.1f MiB", len(body), float64(after.TotalAlloc-before.TotalAlloc)/(1<<20))
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 32<<20 {
		t.Fatalf("opening multiline text allocated %d bytes", allocated)
	}
	if len(r.pages) < 2 || r.editor.Buffer.Len() > textReaderPageBytes {
		t.Fatal("large multiline text was loaded into one editor")
	}
	clickInterface(t, tt, "复制全文")
	if tt.Clipboard() != body {
		t.Fatal("copying full multiline text changed its content")
	}
}

func TestTextPreviewOnlyWhenRowTruncates(t *testing.T) {
	wide := strings.Repeat("宽", 40)
	for _, tc := range []struct {
		name  string
		entry history.Entry
		want  bool
	}{
		{"short", history.Entry{Kind: string(clip.KindText), Text: "短文本"}, false},
		{"lines", history.Entry{Kind: string(clip.KindText), Text: "你好\n世界"}, false},
		{"long", history.Entry{Kind: string(clip.KindText), Text: strings.Repeat("文", 81)}, true},
		{"file", history.Entry{Kind: string(clip.KindText), Text: "短", TextFile: "x.txt"}, true},
		{"wide", history.Entry{Kind: string(clip.KindText), Text: wide}, true},
		{"image", history.Entry{Kind: string(clip.KindImage), Width: 12, Height: 8}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shown := entryTitle(tc.entry)
			var got bool
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Padding(14).Children(func() {
					row := ui.Row(c).Padding(9, 6).Gap(10).AlignItems(ui.Center)
					var title ui.Element
					row.Children(func() {
						ui.Column(c).Size(30, 30)
						ui.Column(c).Grow(1).Children(func() {
							title = ui.Text(c, shown).SingleLine()
						})
					})
					got = rowNeedsPreview(c, tc.entry, shown, title)
				})
			}, 380, 80)
			tt.Frame()
			if got != tc.want {
				t.Fatalf("preview=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestPickTextPreviewCanScrollAndReportLoadFailure(t *testing.T) {
	body := "开头\n" + strings.Repeat("文本内容\n", 200) + "末尾"
	state := newPickTextPreview(body)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			pickTextPreview(c, state, nil)
		})
	}, 396, 300)
	before := bytes.Clone(tt.Image().Pix)
	tt.Scroll(100, 100, 0, 1000)
	if bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("text preview could not scroll beyond its beginning")
	}
	failed := ui.NewTester(func(c *ui.Context) {
		pickTextPreview(c, nil, errors.New("missing body"))
	}, 240, 60)
	if !failed.HasText("文本预览加载失败，请重新悬停重试") {
		t.Fatal("failed preview did not explain how to retry")
	}
}

func TestPickTextPreviewEditorIsReadOnly(t *testing.T) {
	body := "  开头\n\t缩进 😀\n" + strings.Repeat("保留空格  \n", 30)
	state := newPickTextPreview(body)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { pickTextPreview(c, state, nil) })
	}, 396, 300)
	clickInterface(t, tt, "文本预览")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != body {
		t.Fatal("preview editor changed the copied text or whitespace")
	}
	tt.Type("不应写入")
	tt.Key(0, ui.KeyBackspace)
	tt.SetClipboard("不应粘贴")
	tt.Key(ui.Cmd, ui.KeyV)
	if state.Buffer.String() != body {
		t.Fatal("preview editor changed the text")
	}
}

func TestPickTextLanguageRecognizesCodeAndLeavesProsePlain(t *testing.T) {
	for _, tc := range []struct {
		text string
		want string
	}{
		{`{"message": "你好", "count": 42}`, "JSON"},
		{"{\n  \"items\": [\n    1,", "JSON"}, // A truncated preview still highlights.
		{"package main\n\nfunc main() { println(42) }", "Go"},
		{"func (s *Store) Read() string { return \"正文\" }", "Go"},
		{"def greet(name):\n    return \"你好\"", "Python"},
		{"# Greeting\ndef greet(name):\n    return \"你好\"", "Python"},
		{"const count = 42;\nconsole.log(count);", "TypeScript"},
		{"export interface User { name: string }", "TypeScript"},
		{"pub fn main() { println!(\"你好\"); }", "Rust"},
		{"#!/bin/bash\necho \"你好\"", "Shell"},
		{"SELECT name FROM users WHERE id = 42;", "SQL"},
		{"<div class=\"message\">你好</div>", "HTML"},
		{"<?xml version=\"1.0\"?><root />", "XML"},
		{"# 标题\n\n```go\nfunc main() {}\n```", "Markdown"},
		{"你好，今天的会议在下午三点。\n请带上资料。", ""},
		{"Please return the package tomorrow.", ""},
		{"https://github.com/egoist/mygo/releases", ""},
	} {
		t.Run(tc.text, func(t *testing.T) {
			language := pickTextLanguage(tc.text)
			got := ""
			if language != nil {
				got = language.Name
			}
			if got != tc.want {
				t.Fatalf("language=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestPickTextPreviewRendersHighlightAndLineNumbersWithoutCopyingThem(t *testing.T) {
	body := "{\n  \"message\": \"你好\",\n  \"count\": 42\n}"
	render := func(state *editor.State) *ui.Tester {
		return ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Children(func() { pickTextPreview(c, state, nil) })
		}, 396, 300)
	}
	state := newPickTextPreview(body)
	tt := render(state)
	plain := newPickTextPreview(body)
	plain.Buffer.SetLanguage(nil)
	if bytes.Equal(tt.Image().Pix, render(plain).Image().Pix) {
		t.Fatal("recognized code rendered without syntax highlighting")
	}
	unnumbered := newPickTextPreview(body)
	unnumbered.LineNumbers = editor.LineNumbersOff
	if bytes.Equal(tt.Image().Pix, render(unnumbered).Image().Pix) {
		t.Fatal("line numbers were not rendered")
	}
	clickInterface(t, tt, "文本预览")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != body {
		t.Fatal("copying highlighted code changed text or included line numbers")
	}
	tt.Type("不可编辑")
	if state.Buffer.String() != body {
		t.Fatal("highlighted preview was editable")
	}
	reader := &pickTextReader{text: body}
	full := ui.NewTester(reader.view, 720, 480)
	full.Key(ui.Cmd, ui.KeyA)
	full.Key(ui.Cmd, ui.KeyC)
	if full.Clipboard() != body {
		t.Fatal("full reader copied line numbers or changed highlighted code")
	}
	full.Type("不可编辑")
	if reader.editor.Buffer.String() != body {
		t.Fatal("highlighted full reader was editable")
	}
}

func TestPickTextPreviewDoesNotChangeStoredText(t *testing.T) {
	a := interfaceTestApp(t)
	body := "  开头\r\n\t缩进\r\n混合换行\n尾部  "
	item, err := clip.FromText(body)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := a.hist.Add(history.DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	text, _, err := a.hist.TextPreview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := newPickTextPreview(text)
	ui.NewTester(func(c *ui.Context) { pickTextPreview(c, state, nil) }, 396, 300)
	stored, err := a.hist.PasteItem(entry.ID)
	if err != nil || stored.Text != item.Text {
		t.Fatalf("preview changed the stored text: %v", err)
	}
	r := &pickTextReader{text: stored.Text}
	tt := ui.NewTester(r.view, 720, 480)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != item.Text {
		t.Fatal("full reader changed the stored text")
	}
}

func TestPickTextPreviewLongLineMemoryBounded(t *testing.T) {
	for _, body := range []string{
		strings.Repeat("A", 16<<10),
		strings.Repeat("中😀\t ", (16<<10)/9),
	} {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		state := newPickTextPreview(body)
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Children(func() { pickTextPreview(c, state, nil) })
		}, 396, 300)
		tt.SetScale(2)
		runtime.ReadMemStats(&after)
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("previewed %d bytes in %s, allocated %.1f MiB", len(body), time.Since(start), float64(allocated)/(1<<20))
		if allocated > 12<<20 {
			t.Fatalf("hover preview allocated %d bytes", allocated)
		}
	}
}

func TestPickerTextWindowsLoadAndDismiss(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	a := interfaceTestApp(t)
	body := "开头\n" + strings.Repeat("  长文本正文\n", 2000) + "完整尾部"
	item, _ := clip.FromText(body)
	entry, err := a.hist.Add(history.DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mygo.RunOnMain(func() {
			a.closePicker()
			if a.pickOriginal != nil {
				a.pickOriginal.Close()
			}
			a.releaseThumbnails()
			if a.tray != nil {
				a.tray.Destroy()
			}
		})
	})
	startPreview := func() {
		a.openPicker(false)
		a.pickPreview.hover = previewHover{id: entry.ID, since: time.Now().Add(-pickPreviewDelay)}
		a.pickPreview.anchor = mygo.Rectangle{Width: 380, Height: 40}
		a.loadPickPreview(entry.ID, a.picker)
	}
	mygo.RunOnMain(func() {
		a.startTray()
		startPreview()
		// A completed disk read must not reopen a closed history panel.
		a.closePicker()
	})
	waitForTextLoad := func() {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			var loading bool
			mygo.RunOnMain(func() { loading = a.pickLoading })
			if !loading {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("text read did not complete")
	}
	waitForTextLoad()
	mygo.RunOnMain(func() {
		if a.pickPreview.win != nil || a.picker != nil {
			t.Error("late preview reopened the closed picker")
		}
		startPreview()
	})
	waitForTextLoad()
	mygo.RunOnMain(func() {
		if a.pickPreview.win == nil {
			t.Error("text preview did not open")
			return
		}
		if _, err := a.pickPreview.win.CapturePage(); err != nil {
			t.Errorf("render text preview: %v", err)
		}
		a.loadPickText(entry.ID, a.picker)
	})
	waitForTextLoad()
	mygo.RunOnMain(func() {
		first := a.pickOriginal
		if first == nil || a.picker != nil || a.pickPreview.win != nil {
			t.Error("full text did not replace the picker and preview")
			return
		}
		if _, err := first.CapturePage(); err != nil {
			t.Errorf("render full text: %v", err)
		}
		a.openPickText(strings.Repeat("A", clip.MaxTextBytes))
		if !first.IsDestroyed() || a.pickOriginal == first {
			t.Error("opening another text retained the previous viewer")
		}
		if _, err := a.pickOriginal.CapturePage(); err != nil {
			t.Errorf("render segmented long text: %v", err)
		}
		a.pickOriginal.Close()
		if a.pickOriginal != nil {
			t.Error("closed viewer remained attached to the app")
		}
	})
}
