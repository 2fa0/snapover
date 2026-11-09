package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func waitPickerMutation(t *testing.T, a *app, tt *ui.Tester) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for a.historyCleanupPending != nil || a.pickRestore || a.pickerSearch.pending != nil {
		if time.Now().After(deadline) {
			t.Fatal("picker mutation stalled")
		}
		time.Sleep(time.Millisecond)
		tt.Frame()
	}
}

func TestSourceSearchKeepsDistinctContentsVisible(t *testing.T) {
	for _, picker := range []bool{true, false} {
		a := interfaceTestApp(t)
		for _, body := range []string{"不同的第一段内容", "不同的第二段内容"} {
			item, _ := clip.FromText(body)
			item.SourceApp = "Visual Studio Code"
			if _, err := a.hist.Add(history.DirLocal, "", item); err != nil {
				t.Fatal(err)
			}
		}
		a.refreshEntries()
		// List accessibility labels also contain titles. Check the rendered
		// text itself, rather than mistaking those labels for visible content.
		a.pickList.Label, a.histList.Label = nil, nil
		a.pickQuery, a.query, a.page = "studio", "studio", pageHistory
		view, width := a.view, 960
		if picker {
			view, width = a.pickerView, 380
		}
		tt := ui.NewTester(view, width, 440)
		for _, body := range []string{"不同的第一段内容", "不同的第二段内容"} {
			if !tt.HasText(body) {
				t.Fatalf("picker=%v source match hid clipboard content %q: %v", picker, body, tt.Texts())
			}
		}
		if !strings.Contains(strings.Join(tt.Texts(), " "), "来源应用：Visual Studio Code") {
			t.Fatal("source match explanation disappeared")
		}
	}
}

func TestPickerNewQuerySupersedesPendingSelectionRestore(t *testing.T) {
	a := interfaceTestApp(t)
	for _, body := range []string{"new first", "new second", "old selection"} {
		item, _ := clip.FromText(body)
		if _, err := a.hist.Add(history.DirLocal, "", item); err != nil {
			t.Fatal(err)
		}
	}
	a.refreshEntries()
	a.pickSel = 2
	tt := ui.NewTester(a.pickerView, 380, 440)
	// Hold a mutation's completion while the user changes the query.
	a.pickRestore, a.pickRestoreID, a.pickRestoreIndex = true, a.entries[2].ID, 2
	a.historyCleanupPending = make(chan historyCleanupResult, 1)
	a.pickMutationPending = true
	a.pickFlash = "正在处理历史…"
	tt.Type("new")
	a.historyCleanupPending <- historyCleanupResult{message: "已删除"}
	waitPickerMutation(t, a, tt)
	if a.pickQuery != "new" || a.pickSel != 0 {
		t.Fatalf("old mutation overwrote new search selection: query=%q selection=%d", a.pickQuery, a.pickSel)
	}
	if a.pickFlash != "已删除" {
		t.Fatalf("completion left stale progress: %q", a.pickFlash)
	}
}

func TestPickerMenuActsOnClickedRecordAndPreservesSearchSelection(t *testing.T) {
	a := interfaceTestApp(t)
	for _, text := range []string{"matching first", "matching second", "matching third"} {
		item, _ := clip.FromText(text)
		if _, err := a.hist.Add(history.DirLocal, "", item); err != nil {
			t.Fatal(err)
		}
	}
	a.refreshEntries()
	a.pickQuery, a.pickSel = "matching", 0
	tt := ui.NewTester(a.pickerView, 380, 440)
	if err := tt.RightClick("matching second"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("置顶"); err != nil {
		t.Fatal(err)
	}
	waitPickerMutation(t, a, tt)
	shown := a.filtered(a.pickQuery)
	if a.pickQuery != "matching" || shown[a.pickSel].Text != "matching second" || !shown[a.pickSel].Pinned {
		t.Fatal("pin lost query or acted on wrong record")
	}
	if !tt.HasText(pinShortcutLabel(1)) {
		t.Fatal("pinned shortcut is not discoverable")
	}
	if err := tt.RightClick("matching second"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("删除"); err != nil {
		t.Fatal(err)
	}
	waitPickerMutation(t, a, tt)
	shown = a.filtered(a.pickQuery)
	if len(shown) != 2 || a.pickSel != 1 || shown[1].Text != "matching first" || a.pickQuery != "matching" {
		t.Fatalf("delete did not keep adjacent selection: %d %+v", a.pickSel, shown)
	}
}

func TestPickerModifiedEnterOnlyCopiesAndPlainBackspaceEditsSearch(t *testing.T) {
	a := interfaceTestApp(t)
	a.picker = &mygo.Window{}
	board := &slowPreparedBoard{started: make(chan struct{}), finish: make(chan struct{})}
	a.board = board
	item, _ := clip.FromText("copy me")
	entry, err := a.hist.Add(history.DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	a.pickQuery = "copy"
	tt := ui.NewTester(a.pickerView, 380, 440)
	tt.Key(0, ui.KeyBackspace)
	if a.pickQuery != "cop" || len(a.entries) != 1 {
		t.Fatalf("Backspace did not edit search: %q", a.pickQuery)
	}
	tt.Key(ui.Alt, ui.KeyEnter)
	r := a.historyPaste
	if r == nil || !r.copyOnly || r.id != entry.ID {
		t.Fatal("modified Enter started automatic paste")
	}
	close(board.finish)
	select {
	case result := <-r.result:
		r.result <- result
	case <-time.After(time.Second):
		t.Fatal("copy stalled")
	}
	a.status = "unchanged"
	a.completeHistoryPaste(r)
	if len(board.writes) != 1 || board.writes[0].Text != item.Text || a.status != "unchanged" {
		t.Fatal("copy did not publish once or tried to deliver a paste")
	}
}

func TestPickerPinShortcutAndFixedKeyIgnoreRowOrder(t *testing.T) {
	a := interfaceTestApp(t)
	a.picker = &mygo.Window{}
	board := &slowPreparedBoard{started: make(chan struct{}), finish: make(chan struct{})}
	a.board = board
	item, _ := clip.FromText("pinned text")
	entry, err := a.hist.Add(history.DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	tt := ui.NewTester(a.pickerView, 380, 440)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	waitPickerMutation(t, a, tt)
	item, _ = clip.FromText("newer text")
	if _, err := a.hist.Add(history.DirLocal, "", item); err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	tt.Frame()
	tt.Key(ui.Cmd, ui.Key1)
	if a.historyPaste == nil || a.historyPaste.id != entry.ID || a.historyPaste.copyOnly {
		t.Fatal("fixed pin key followed row order")
	}
	r := a.historyPaste
	a.cancelHistoryPaste()
	close(board.finish)
	select {
	case result := <-r.result:
		result.prepared.Close()
	case <-time.After(time.Second):
		t.Fatal("paste preparation stalled")
	}
}

func TestPickerFileMenuAndBusyMutationGuards(t *testing.T) {
	a := interfaceTestApp(t)
	entry := historyFileEntry(t, a, true)
	revealed := make(chan string, 1)
	restore := revealFileInFolder
	revealFileInFolder = func(path string) { revealed <- path }
	t.Cleanup(func() { revealFileInFolder = restore })

	tt := ui.NewTester(a.pickerView, 380, 440)
	if err := tt.RightClick(entry.FileName); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(revealFileLabel()); err != nil {
		t.Fatal(err)
	}
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
	a.fileAvailability = map[string]fileAvailabilityResult{entry.ID: {id: entry.ID, available: false, at: time.Now()}}
	tt.Frame()
	if err := tt.RightClick(entry.FileName); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("只复制"); err == nil {
		t.Fatal("missing file metadata offered as a complete copy")
	}
	a.historyCleanupPending = make(chan historyCleanupResult, 1)
	a.performPickerAction(entry.ID, "delete")
	if len(a.hist.List()) != 1 || a.pickRestore {
		t.Fatal("concurrent cleanup allowed deletion")
	}
}

func TestPinnedNoteEditorAndSearchInPickerAndMainHistory(t *testing.T) {
	a := interfaceTestApp(t)
	item, _ := clip.FromText("https://internal.example.com/admin")
	entry, err := a.hist.Add(history.DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.hist.SetPinned(entry.ID, true); err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()

	tt := ui.NewTester(a.pickerView, 380, 440)
	if err := tt.RightClick(entry.Text); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("设置备注…"); err != nil {
		t.Fatal(err)
	}
	if a.editNoteID != entry.ID {
		t.Fatalf("note editor did not open for %s", entry.ID)
	}
	a.editNoteText = "内网管理后台"
	tt.Frame()
	if err := tt.Click("保存备注"); err != nil {
		t.Fatal(err)
	}
	waitPickerMutation(t, a, tt)
	if !strings.Contains(strings.Join(tt.Texts(), " "), "备注：内网管理后台") {
		t.Fatalf("picker did not render saved note: %v", tt.Texts())
	}

	// Search by note in main history page and verify shortcut badge + note match detail.
	a.page = pageHistory
	a.query = "管理后台"
	mainTT := ui.NewTester(a.view, 960, 560)
	if !mainTT.HasText(entry.Text) || !mainTT.HasText(pinShortcutLabel(1)) {
		t.Fatalf("main history missing note search hit or shortcut badge: %v", mainTT.Texts())
	}
	if !strings.Contains(strings.Join(mainTT.Texts(), " "), "备注：内网管理后台") {
		t.Fatalf("main history missing note match detail: %v", mainTT.Texts())
	}

	// Clear note via main history context menu.
	if err := mainTT.RightClick(entry.Text); err != nil {
		t.Fatal(err)
	}
	if err := mainTT.ChooseMenuItem("清除备注"); err != nil {
		t.Fatal(err)
	}
	waitPickerMutation(t, a, mainTT)
	if got := a.hist.List()[0]; got.Note != "" {
		t.Fatalf("note was not cleared: %+v", got)
	}
}
