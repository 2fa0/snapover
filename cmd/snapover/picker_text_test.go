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

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
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

func TestPickTextPreviewCanScrollAndReportLoadFailure(t *testing.T) {
	body := "开头\n" + strings.Repeat("文本内容\n", 200) + "末尾"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			pickTextPreview(c, body, nil)
		})
	}, 396, 300)
	before := bytes.Clone(tt.Image().Pix)
	tt.Scroll(100, 100, 0, -1000)
	if bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("text preview could not scroll beyond its beginning")
	}
	failed := ui.NewTester(func(c *ui.Context) {
		pickTextPreview(c, "", errors.New("missing body"))
	}, 240, 60)
	if !failed.HasText("文本预览加载失败，请重新悬停重试") {
		t.Fatal("failed preview did not explain how to retry")
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
		a.loadPickPreview(entry.ID)
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
