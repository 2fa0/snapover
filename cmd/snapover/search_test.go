package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
)

func TestSearchShowsFieldMatchesWhileTheDiskSearchReads(t *testing.T) {
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, _ := clip.FromText("标题关键字 " + strings.Repeat("填充内容 ", 300) + "needle")
	if _, err := store.Add(history.DirLocal, "", item); err != nil {
		t.Fatal(err)
	}
	a := &app{hist: store, root: context.Background(), entries: store.List()}
	if a.entries[0].TextFile == "" {
		t.Fatal("body was not offloaded, so no disk search would run")
	}
	// The loaded title matches at once; the row does not wait for the body.
	shown := a.filtered("标题关键字")
	if len(shown) != 1 || a.pickerSearch.pending == nil {
		t.Fatalf("field match showed %d rows with pending=%v", len(shown), a.pickerSearch.pending)
	}
	// The disk search then takes over, still with the match marked.
	deadline := time.Now().Add(3 * time.Second)
	for a.pickerSearch.pending != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		a.filtered("标题关键字")
	}
	shown = a.filtered("标题关键字")
	if len(shown) != 1 || len(shown[0].MatchRanges) == 0 || a.pickerSearch.err != nil {
		t.Fatalf("disk result = %+v, err=%v", shown, a.pickerSearch.err)
	}
	// A body-only match is still the disk search's: nothing local to show.
	if len(a.filtered("needle")) != 0 || a.pickerSearch.pending == nil {
		t.Fatal("body match was not left to the disk search")
	}
}

func TestSearchCountLabelReportsPendingReads(t *testing.T) {
	var s historySearch
	if got := s.countLabel(3); got != "3 条" {
		t.Fatalf("count = %q", got)
	}
	s.pending = make(chan searchResult, 1)
	if got := s.countLabel(3); got != "搜索中…" {
		t.Fatalf("count while reading = %q", got)
	}
}

func TestBackgroundSearchLatestQueryAndRefresh(t *testing.T) {
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"needle", "other"} {
		item, _ := clip.FromText(strings.Repeat("A", 1000) + word)
		if _, err := store.Add(history.DirLocal, "", item); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &app{hist: store, root: ctx, entries: store.List()}
	a.filtered("needle")
	old := a.pickerSearch.pending
	a.filtered("other")
	if a.pickerSearch.pending == old {
		t.Fatal("old request reused")
	}
	deadline := time.Now().Add(3 * time.Second)
	for a.pickerSearch.pending != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		a.filtered("other")
	}
	if len(a.filtered("other")) != 1 || a.pickerSearch.err != nil {
		t.Fatal("latest query not returned")
	}
	entry := a.filtered("other")[0]
	body, err := store.Item(entry.ID)
	if err != nil || !strings.HasSuffix(body.Text, "other") {
		t.Fatal("stale query results returned")
	}
	// History and picker searches have separate queues and results.
	a.query = "needle"
	a.shown()
	deadline = time.Now().Add(3 * time.Second)
	for a.historySearch.pending != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		a.shown()
	}
	if len(a.shown()) != 1 || a.shown()[0].ID == entry.ID {
		t.Fatal("view queries interfered")
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	if len(a.filtered("other")) != 0 || len(a.shown()) != 0 {
		t.Fatal("refresh retained old results")
	}
}
