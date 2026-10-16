package main

import (
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
)

func TestPickerConsumesCompletedCleanupWithoutMainWindow(t *testing.T) {
	a := interfaceTestApp(t)
	item, _ := clip.FromText("keep this record")
	entry, err := a.hist.Add("local", "", item)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	a.startHistoryCleanup(false, 64<<20)
	deadline := time.Now().Add(3 * time.Second)
	for len(a.historyCleanupPending) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("cleanup stalled")
		}
		time.Sleep(time.Millisecond)
	}
	_ = ui.NewTester(a.pickerView, 380, 440)
	if a.win != nil || a.historyCleanupPending != nil {
		t.Fatal("completed cleanup still blocks independent picker")
	}
	a.board = &pasteTestBoard{}
	a.startHistoryPaste(entry.ID, nil)
	if a.historyPaste == nil {
		t.Fatal("paste still blocked after cleanup")
	}
	r := a.historyPaste
	a.cancelHistoryPaste()
	select {
	case result := <-r.result:
		result.prepared.Close()
	case <-time.After(time.Second):
		t.Fatal("cancelled paste stalled")
	}
}

type slowPreparedBoard struct {
	pasteTestBoard
	started, finish chan struct{}
	once            sync.Once
}

func (b *slowPreparedBoard) PrepareWrite(item clip.Item) (clip.PreparedWrite, error) {
	b.once.Do(func() { close(b.started) })
	<-b.finish
	return clip.PreparedWrite{Publish: func() error { return b.Write(item) }}, nil
}

func TestCancelledPasteKeepsOnlyOnePreparingWorkerAcrossWindowLifetimes(t *testing.T) {
	a := interfaceTestApp(t)
	board := &slowPreparedBoard{started: make(chan struct{}), finish: make(chan struct{})}
	a.board = board
	item, _ := clip.FromText("only one pending paste")
	entry, err := a.hist.Add("local", "", item)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	a.startHistoryPaste(entry.ID, nil)
	r := a.historyPaste
	<-board.started
	a.cancelHistoryPaste()
	a.startHistoryPaste(entry.ID, nil)
	if a.historyPaste == nil || a.queuedPaste != a.historyPaste || a.pasteWorker != r {
		t.Fatal("new worker started while cancelled preparation was still running")
	}
	a.cancelHistoryPaste()
	close(board.finish)
	select {
	case result := <-r.result:
		r.result <- result
	case <-time.After(time.Second):
		t.Fatal("preparation did not finish")
	}
	a.completeHistoryPaste(r)
	if a.pasteWorker != nil || len(board.writes) != 0 {
		t.Fatal("cancelled result leaked or published")
	}
}

func TestNewestPasteStartsAfterCancelledWorkerFinishes(t *testing.T) {
	a := interfaceTestApp(t)
	board := &slowPreparedBoard{started: make(chan struct{}), finish: make(chan struct{})}
	a.board = board
	item, _ := clip.FromText("newest paste intent")
	entry, err := a.hist.Add("local", "", item)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	a.startHistoryPaste(entry.ID, nil)
	first := a.historyPaste
	<-board.started
	a.cancelHistoryPaste()
	a.startHistoryPaste(entry.ID, nil)
	newest := a.historyPaste
	close(board.finish)
	select {
	case result := <-first.result:
		first.result <- result
	case <-time.After(time.Second):
		t.Fatal("cancelled worker stalled")
	}
	a.completeHistoryPaste(first)
	if a.pasteWorker != newest || a.queuedPaste != nil {
		t.Fatal("latest paste intent did not advance")
	}
	select {
	case result := <-newest.result:
		newest.result <- result
	case <-time.After(time.Second):
		t.Fatal("latest paste preparation stalled")
	}
	a.completeHistoryPaste(newest)
	if len(board.writes) != 1 || a.historyPaste != nil || a.pasteWorker != nil {
		t.Fatal("latest paste did not publish once")
	}
}
