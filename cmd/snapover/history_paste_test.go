package main

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
)

type pasteTestBoard struct {
	writes []clip.Item
	err    error
}

func (*pasteTestBoard) Sequence() (uint64, error)    { return 0, nil }
func (*pasteTestBoard) Snapshot() (clip.Item, error) { return clip.Item{}, nil }
func (b *pasteTestBoard) Write(item clip.Item) error {
	if b.err != nil {
		return b.err
	}
	b.writes = append(b.writes, item)
	return nil
}

func TestHistoryPasteLoadsBeforePublishingOnUIThread(t *testing.T) {
	a := interfaceTestApp(t)
	board := &pasteTestBoard{}
	a.board = board
	item, _ := clip.FromText("  完整正文\n")
	entry, err := a.hist.Add("local", "", item)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	a.startHistoryPaste(entry.ID, nil)
	r := a.historyPaste
	if r == nil || len(board.writes) != 0 {
		t.Fatal("paste should return before reading or publishing the item")
	}
	select {
	case result := <-r.result:
		r.result <- result
	case <-time.After(3 * time.Second):
		t.Fatal("background read did not finish")
	}
	a.completeHistoryPaste(r)
	if a.historyPaste != nil || len(board.writes) != 1 || board.writes[0].Text != item.Text {
		t.Fatal("background result was not published exactly once")
	}
	a.completeHistoryPaste(r)
	if len(board.writes) != 1 {
		t.Fatal("late callback repeated the clipboard write")
	}
}

func TestHistoryPasteRejectsCancelledDeletedAndFailedRequests(t *testing.T) {
	for _, reason := range []string{"cancelled", "deleted", "picker closed", "read failed", "write failed"} {
		t.Run(reason, func(t *testing.T) {
			a := interfaceTestApp(t)
			board := &pasteTestBoard{}
			a.board = board
			item, _ := clip.FromText("keep clipboard")
			entry, err := a.hist.Add("local", "", item)
			if err != nil {
				t.Fatal(err)
			}
			a.refreshEntries()
			r := &pasteRequest{id: entry.ID, result: make(chan pasteResult, 1)}
			result := pasteResult{item: item}
			a.historyPaste = r
			switch reason {
			case "cancelled":
				a.historyPaste = nil
			case "deleted":
				a.entries = nil
			case "picker closed":
				r.picker = &mygo.Window{}
			case "read failed":
				result.err = errors.New("disk read failed")
			case "write failed":
				board.err = errors.New("clipboard busy")
			}
			r.result <- result
			a.completeHistoryPaste(r)
			if len(board.writes) != 0 {
				t.Fatal("invalid request overwrote the clipboard")
			}
		})
	}
}

func TestPickerEscapeCancelsReadyPasteBeforeClipboardWrite(t *testing.T) {
	a := interfaceTestApp(t)
	board := &pasteTestBoard{}
	a.board = board
	item, _ := clip.FromText("keep clipboard")
	entry, err := a.hist.Add("local", "", item)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	a.picker = &mygo.Window{}
	r := &pasteRequest{id: entry.ID, picker: a.picker, result: make(chan pasteResult, 1)}
	a.historyPaste, a.pickBusy = r, true
	tt := ui.NewTester(a.pickerView, 380, 440)
	r.result <- pasteResult{item: item}
	tt.Key(0, ui.KeyEscape)
	a.completeHistoryPaste(r)
	if len(board.writes) != 0 || a.historyPaste != nil {
		t.Fatal("Escape did not cancel the ready paste before publishing it")
	}
}
