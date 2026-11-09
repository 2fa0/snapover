package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	clipsync "github.com/2fa0/snapover/internal/sync"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func historyFileEntry(t *testing.T, a *app, folder bool) history.Entry {
	t.Helper()
	p := filepath.Join(t.TempDir(), "报告.pdf")
	if folder {
		p = filepath.Join(t.TempDir(), "项目资料")
		if err := os.MkdirAll(filepath.Join(p, "子目录"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "子目录", "不展开.txt"), []byte("nested"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "README.md"), []byte("snapshot"), 0600); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(p, []byte("snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromFiles([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := a.hist.Add(history.DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	return entry
}

func TestQuickHistoryFileNamesStayCompactAndFolderHoverShowsPreview(t *testing.T) {
	for _, folder := range []bool{false, true} {
		t.Run(fmt.Sprint(folder), func(t *testing.T) {
			a := interfaceTestApp(t)
			entry := historyFileEntry(t, a, folder)
			a.picker = new(mygo.Window)
			a.pickLoading = true
			tt := ui.NewTester(a.pickerView, 380, 440)
			if !tt.HasText(entry.FileName) || tt.HasText("README.md") || tt.HasText("不展开.txt") {
				t.Fatal("row should show only the copied file or folder name")
			}
			r, ok := tt.Find(entry.FileName)
			if !ok {
				t.Fatal("missing filename")
			}
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
			if got := a.pickPreview.hover.id != ""; got != folder {
				t.Fatalf("folder hover=%v, want %v", got, folder)
			}
			tt.Key(0, ui.KeyDown)
			if a.pickSel != 0 {
				t.Fatal("file keyboard selection changed")
			}
		})
	}
}

func TestFileTreePreviewUsesOnlyOneLevelAndSignalsMore(t *testing.T) {
	a := interfaceTestApp(t)
	entry := historyFileEntry(t, a, true)
	preview, err := a.hist.FilePreview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(12).Gap(8).Children(func() { pickFilePreview(c, preview, nil) })
	}, 396, 300)
	if !tt.HasText("项目资料") || !tt.HasText("README.md") || !tt.HasText("子目录") || tt.HasText("不展开.txt") || !tt.HasText("仅展示一层，最多 20 项") {
		t.Fatal("folder tree did not match the saved direct children")
	}
	preview.More = true
	tt.Frame()
	if !tt.HasText("还有更多项目 · 仅展示一层，最多 20 项") {
		t.Fatal("truncated tree not explained")
	}
	preview.Items = nil
	preview.More = false
	tt.Frame()
	if !tt.HasText("└─ 空文件夹") {
		t.Fatal("empty folder not explained")
	}
}

func TestFileHistoryPasteWritesExistingFilesAndExplainsWhenRemoved(t *testing.T) {
	a := interfaceTestApp(t)
	entry := historyFileEntry(t, a, true)
	board := &pasteTestBoard{}
	a.board = board
	a.startHistoryPaste(entry.ID, nil)
	r := a.historyPaste
	if r == nil {
		t.Fatal("existing file history paste did not start")
	}
	select {
	case result := <-r.result:
		r.result <- result
	case <-time.After(3 * time.Second):
		t.Fatal("background file paste read did not finish")
	}
	a.completeHistoryPaste(r)
	if len(board.writes) != 1 || board.writes[0].Kind != clip.KindFiles || len(board.writes[0].Files) != 1 {
		t.Fatalf("existing file history did not write back files: %+v", board.writes)
	}
	for _, p := range entry.FilePaths {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	board.writes = nil
	a.startHistoryPaste(entry.ID, nil)
	finishFilePaste(t, a)
	if a.historyPaste != nil || len(board.writes) != 0 {
		t.Fatal("removed file history paste started a body copy")
	}
	if a.flash != "文件内容未保存在历史中，请重新复制后再粘贴" {
		t.Fatalf("missing explanation: %q", a.flash)
	}
}

func TestFileHistoryPasteFromPickerAndRevealInFolder(t *testing.T) {
	a := interfaceTestApp(t)
	entry := historyFileEntry(t, a, false)
	revealed := make(chan string, 1)
	restore := revealFileInFolder
	revealFileInFolder = func(path string) { revealed <- path }
	t.Cleanup(func() { revealFileInFolder = restore })

	a.revealHistoryFile(entry)
	select {
	case path := <-revealed:
		if path != entry.FilePaths[0] {
			t.Fatal(path)
		}
	case <-time.After(time.Second):
		t.Fatal("reveal worker did not finish")
	}

	for _, p := range entry.FilePaths {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	a.picker = &mygo.Window{}
	board := &pasteTestBoard{}
	a.board = board
	a.startHistoryPaste(entry.ID, a.picker)
	finishFilePaste(t, a)
	if a.historyPaste != nil || a.pickBusy || len(board.writes) != 0 {
		t.Fatal("removed file history paste started a body copy")
	}
	if a.pickFlash != "文件内容未保存在历史中，请重新复制后再粘贴" {
		t.Fatalf("missing picker explanation: %q", a.pickFlash)
	}
}

func TestHistoryFileFilterAndPinDelete(t *testing.T) {
	a := interfaceTestApp(t)
	entry := historyFileEntry(t, a, false)
	text, _ := clip.FromText("text record")
	if _, err := a.hist.Add(history.DirLocal, "", text); err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	a.page = pageHistory
	tt := ui.NewTester(a.view, 960, 640)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	clickInterface(t, tt, "文件")
	if list := a.shown(); len(list) != 1 || list[0].ID != entry.ID {
		t.Fatal("file filter failed", list)
	}
	clickInterface(t, tt, entry.FileName)
	clickInterface(t, tt, "置顶当前记录")
	waitHistoryCleanup(t, a)
	if list := a.hist.List(); len(list) != 2 || !list[1].Pinned {
		t.Fatal("file pin failed", list)
	}
	clickInterface(t, tt, entry.FileName)
	clickInterface(t, tt, "删除")
	waitHistoryCleanup(t, a)
	if list := a.hist.List(); len(list) != 1 || list[0].Kind != "text" {
		t.Fatal("file delete affected text", list)
	}
}

func TestNativeQuickHistoryFolderPreviewAndFileNotices(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	a := interfaceTestApp(t)
	dir := filepath.Join(t.TempDir(), "项目资料")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("报告-%02d.txt", i)), []byte("snapshot"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	item, err := clip.FromFiles([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{clipsync.NoticeLocal, clipsync.NoticeRemote} {
		if err := a.onNotice(clipsync.Notice{Kind: kind, Item: item, PeerName: "另一台电脑"}); err != nil {
			t.Fatal(err)
		}
	}
	var parent *mygo.Window
	mygo.RunOnMain(func() {
		if len(a.entries) != 2 || a.entries[0].Kind != "files" || a.entries[0].Dir != history.DirRemote {
			t.Error("local/remote files missing from quick history")
			return
		}
		parent = mygo.NewWindow(mygo.WindowOptions{Title: "文件历史预览测试", Hidden: true, Width: 380, Height: 440, Content: ui.View(func(c *ui.Context) { ui.Text(c, "隔离预览测试") })})
		a.picker = parent
		id := a.entries[0].ID
		a.pickPreview.hover = previewHover{id: id, since: time.Now().Add(-pickPreviewDelay)}
		a.pickPreview.anchor = mygo.Rectangle{X: 0, Y: 70, Width: 380, Height: 60}
		a.loadPickPreview(id, parent)
	})
	t.Cleanup(func() {
		mygo.RunOnMain(func() {
			a.closePickPreview()
			if parent != nil {
				parent.Close()
			}
			a.picker = nil
		})
	})
	waitForImageOpen(t, a)
	mygo.RunOnMain(func() {
		if a.pickPreview.win == nil {
			t.Error("folder hover preview did not open")
			return
		}
		png, err := a.pickPreview.win.CapturePage()
		if err != nil {
			t.Error(err)
			return
		}
		if dest := os.Getenv("SNAPOVER_TEST_PREVIEW_PNG"); dest != "" {
			if err := os.WriteFile(dest, png, 0600); err != nil {
				t.Error(err)
			}
		}
		a.closePickPreview()
		if a.pickPreview.win != nil || a.pickPreview.request != nil {
			t.Error("closing preview retained resources")
		}
	})
}

func finishFilePaste(t *testing.T, a *app) {
	t.Helper()
	r := a.historyPaste
	if r == nil {
		t.Fatal("background validation did not start")
	}
	select {
	case result := <-r.result:
		r.result <- result
	case <-time.After(3 * time.Second):
		t.Fatal("file validation did not finish")
	}
	a.completeHistoryPaste(r)
}
