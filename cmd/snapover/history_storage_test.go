package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo/ui"
)

func waitHistoryCleanup(t *testing.T, a *app) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for a.historyCleanupPending != nil {
		a.pollHistoryStorage()
		if time.Now().After(deadline) {
			t.Fatal("history cleanup did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestHistoryCapacitySettingsPersistAndPrune(t *testing.T) {
	a := interfaceTestApp(t)
	item, _ := clip.FromText(strings.Repeat("x", 600<<10))
	oldest, err := a.hist.Add(history.DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	newest, err := a.hist.Add(history.DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	a.historyMaxText = " 1 "
	a.saveHistoryCapacity()
	// A repeated action while the first is pending must not change the limit.
	a.historyMaxText = "2"
	a.saveHistoryCapacity()
	waitHistoryCleanup(t, a)
	if a.historyCapacityError != "" {
		t.Fatal(a.historyCapacityError)
	}
	got, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || got.Values.HistoryMaxMB != 1 {
		t.Fatalf("limit was not saved: %+v %v", got, err)
	}
	if len(a.entries) != 1 || a.entries[0].ID != newest.ID || a.entries[0].ID == oldest.ID {
		t.Fatal("settings did not refresh pruned history")
	}
	for _, value := range []string{"", "0", "-1", "1.5", "abc", "1048577"} {
		a.historyMaxText = value
		a.saveHistoryCapacity()
		if a.historyCapacityError == "" || a.cfg.Values.HistoryMaxMB != 1 || a.historyCleanupPending != nil {
			t.Fatalf("invalid limit accepted: %q", value)
		}
	}
}

func TestHistoryCapacitySaveFailureDoesNotDeleteData(t *testing.T) {
	a := interfaceTestApp(t)
	item, _ := clip.FromText("keep")
	if _, err := a.hist.Add(history.DirLocal, "", item); err != nil {
		t.Fatal(err)
	}
	before := a.cfg.Values.HistoryMaxMB
	if err := os.Mkdir(a.cfg.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	a.historyMaxText = "1"
	a.saveHistoryCapacity()
	if a.historyCapacityError == "" || a.cfg.Values.HistoryMaxMB != before || len(a.hist.List()) != 1 || a.historyCleanupPending != nil {
		t.Fatal("save failure changed live history")
	}
}

func TestStorageSettingsDoNotClearHistory(t *testing.T) {
	a := interfaceTestApp(t)
	item, _ := clip.FromText("keep")
	if _, err := a.hist.Add(history.DirLocal, "", item); err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Gap(12).Children(func() { a.historyStorageSettings(c) }) }, 850, 600)
	if !tt.HasText("刷新占用") || !tt.HasText("历史容量上限，MB") {
		t.Fatal("storage settings lost capacity controls")
	}
	for _, label := range []string{"清理全部未置顶历史", "确认清理未置顶历史", "取消清理"} {
		if tt.HasText(label) {
			t.Fatalf("settings still expose history clearing: %s", label)
		}
	}
}

func TestHistoryCleanupCancelsPreparedPaste(t *testing.T) {
	a := interfaceTestApp(t)
	board := &pasteTestBoard{}
	a.board = board
	item, _ := clip.FromText("must not paste")
	entry, err := a.hist.Add(history.DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	request := &pasteRequest{id: entry.ID, result: make(chan pasteResult, 1)}
	request.result <- pasteResult{item: item}
	a.historyPaste, a.pickBusy = request, true
	a.startHistoryCleanup(true, 0)
	a.startHistoryPaste(entry.ID, nil)
	if a.historyPaste != nil {
		t.Fatal("cleanup accepted a new paste before the list refreshed")
	}
	a.completeHistoryPaste(request)
	waitHistoryCleanup(t, a)
	if len(board.writes) != 0 || a.pickBusy || a.historyPaste != nil {
		t.Fatal("cleanup allowed a prepared paste to publish")
	}
}

func TestHistoryStorageIgnoresStaleErrorsAndRefreshesPartialCleanup(t *testing.T) {
	a := interfaceTestApp(t)
	a.historyUsageVersion = 2
	a.historyUsagePending = make(chan historyUsageResult, 1)
	a.historyUsagePending <- historyUsageResult{version: 1, err: os.ErrPermission}
	a.pollHistoryStorage()
	if a.historyUsageValid || a.historyUsageError != "" {
		t.Fatal("stale error suppressed fresh statistics")
	}
	item, _ := clip.FromText("removed")
	if _, err := a.hist.Add(history.DirLocal, "", item); err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	if err := a.hist.Clear(); err != nil {
		t.Fatal(err)
	}
	a.historyCleanupPending = make(chan historyCleanupResult, 1)
	a.historyCleanupPending <- historyCleanupResult{cleared: true, err: os.ErrPermission}
	a.pollHistoryStorage()
	if len(a.entries) != 0 || a.historyCapacityError == "" || a.historyUsageValid {
		t.Fatal("partial cleanup did not refresh live entries")
	}
}

func TestHistoryQuotaBarShowsUsedOfLimit(t *testing.T) {
	a := interfaceTestApp(t)
	a.cfg.Values.HistoryMaxMB = 2
	a.historyUsageValid = true
	a.historyUsage = history.Usage{ContentBytes: 1 << 20, PinnedBytes: 256 << 10, TotalBytes: (1 << 20) + 8}
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Gap(12).Children(func() { a.historyStorageSettings(c) }) }, 850, 600)
	if !tt.HasText("历史内容占用") || !tt.HasText("1.0 MB / 2.0 MB") {
		t.Fatal("quota bar did not show used and limit")
	}
	if tt.HasText("历史内容 1.0 MB，其中置顶 256.0 KB") || tt.HasText("置顶内容") || tt.HasText("历史目录") {
		t.Fatal("storage summary duplicates usage or exposes collapsed details")
	}
	clickInterface(t, tt, "占用明细")
	t.Cleanup(a.cancelDiskUsage)
	if !tt.HasText("置顶内容") || !tt.HasText("256.0 KB") || !tt.HasText("历史目录") {
		t.Fatal("expanded details lost pinned or disk usage")
	}
	a.historyUsage.PinnedBytes = 3 << 20
	a.historyUsage.ContentBytes = 3 << 20
	tt.Frame()
	if !tt.HasText("3.0 MB / 2.0 MB") || !tt.HasText("置顶内容已超过容量上限，请取消部分置顶或手动删除。") {
		t.Fatal("over-limit usage lost the quota text or pinned warning")
	}
}

func TestStorageRefreshUpdatesHistoryAndExpandedDetails(t *testing.T) {
	a := interfaceTestApp(t)
	t.Cleanup(a.cancelDiskUsage)
	a.historyUsageValid = true
	a.historyUsage.ContentBytes = 1 << 20
	a.showDiskUsage, a.diskUsageValid = true, true
	a.diskUsage = diskUsageResult{receiveDir: a.engine.ReceiveDir(), cache: 1 << 20}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Gap(12).Children(func() { a.historyStorageSettings(c) })
	}, 850, 600)
	clickInterface(t, tt, "刷新占用")
	deadline := time.Now().Add(3 * time.Second)
	for !a.historyUsageValid || !a.diskUsageValid || a.historyUsagePending != nil || a.diskUsagePending != nil {
		tt.Frame()
		if time.Now().After(deadline) {
			t.Fatal("refresh did not finish history and file statistics")
		}
		time.Sleep(time.Millisecond)
	}
	if a.historyUsage.ContentBytes != 0 || a.diskUsage.cache != 0 {
		t.Fatal("refresh retained stale history or file statistics")
	}
}
