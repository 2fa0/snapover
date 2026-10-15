package sync

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
)

var errClipboardBusy = errors.New("mygo: OleSetClipboard failed (HRESULT 0x800401d0)")

type busyBoard struct {
	*memBoard
	failures int
	attempts int
}

func (b *busyBoard) Write(item clip.Item) error {
	b.attempts++
	if b.attempts <= b.failures {
		return errClipboardBusy
	}
	return b.memBoard.Write(item)
}

func TestBusyClipboardRetriesAndSavesImage(t *testing.T) {
	for _, failures := range []int{2, 100} {
		t.Run(strconv.Itoa(failures), func(t *testing.T) {
			board := &busyBoard{memBoard: &memBoard{}, failures: failures}
			e := testEngine(t, board)
			store, err := history.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			e.OnNotice = func(n Notice) error {
				if n.Kind == NoticeRemote {
					_, err := store.Add(history.DirRemote, n.PeerName, n.Item)
					return err
				}
				return nil
			}
			// Main-thread callbacks must return before the retry delay, with
			// the engine lock available between callbacks.
			e.RunOnBoard = func(fn func()) {
				fn()
				if !e.mu.TryLock() {
					t.Fatal("retry held the engine lock outside the board callback")
				}
				e.mu.Unlock()
			}
			item := mustPNG(t)
			err = e.deliverFrom(nil, item)
			if failures == 2 {
				if err != nil || board.attempts != 3 || len(board.Writes()) != 1 || !e.Guard.IsEcho(item.Hash) {
					t.Fatalf("transient failure was not recovered: attempts=%d err=%v", board.attempts, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "已存入对端历史") || board.attempts != 8 || e.Guard.IsEcho(item.Hash) {
				t.Fatalf("permanent contention lost its fallback: attempts=%d err=%v", board.attempts, err)
			}
			entries := store.List()
			if len(entries) != 1 {
				t.Fatalf("expected one remote history item, got %d", len(entries))
			}
			back, err := store.Item(entries[0].ID)
			if err != nil || back.Hash != item.Hash {
				t.Fatalf("saved image changed: %v", err)
			}
		})
	}
}

func TestClipboardRetryRechecksLocalCopyPauseAndSession(t *testing.T) {
	for _, action := range []string{"local-copy", "pause", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			board := &busyBoard{memBoard: &memBoard{}, failures: 1}
			e := testEngine(t, board)
			session := newSession(nil, "peer", true, discardLog(), nil)
			e.sess = session
			callbacks, saved := 0, 0
			e.OnNotice = func(n Notice) error {
				if n.Kind == NoticeRemote {
					saved++
				}
				return nil
			}
			local := mustText(t, "new local copy before the watcher settles")
			e.RunOnBoard = func(fn func()) {
				callbacks++
				if callbacks == 2 {
					switch action {
					case "local-copy":
						board.memBoard.Write(local)
					case "pause":
						e.SetPolicy(true, false, 0)
					case "disconnect":
						e.mu.Lock()
						e.sess = nil
						e.mu.Unlock()
					}
				}
				fn()
			}
			item := mustPNG(t)
			if err := e.deliverFrom(session, item); err == nil || board.attempts != 1 || e.Guard.IsEcho(item.Hash) {
				t.Fatalf("retry escaped changed state: attempts=%d err=%v", board.attempts, err)
			}
			if action == "local-copy" {
				back, _ := board.Snapshot()
				if back.Hash != local.Hash || saved != 1 {
					t.Fatal("new local copy was overwritten or remote history was lost")
				}
			} else if saved != 0 {
				t.Fatal("paused or removed session saved content")
			}
		})
	}
}

func TestFailedAutomaticImageCanBeCopiedAgain(t *testing.T) {
	a, b := newPair(t)
	notices := make(chan Notice, 16)
	a.mu.Lock()
	a.OnNotice = func(n Notice) error { notices <- n; return nil }
	a.mu.Unlock()
	b.mu.Lock()
	board := b.Board.(*memBoard)
	b.Board = &failedBoard{board}
	b.mu.Unlock()
	item := mustPNG(t)
	a.HandleLocal(1, item)
	if n := waitClipboardResult(t, notices); n.ClipboardState != "error" {
		t.Fatal(n)
	}
	b.mu.Lock()
	b.Board = board
	b.mu.Unlock()
	a.HandleLocal(2, item)
	if n := waitClipboardResult(t, notices); n.ClipboardState != "complete" {
		t.Fatal(n)
	}
	waitWrite(t, board, item.Hash)
}

func TestOldFailureCannotResetNewCopyDeduplication(t *testing.T) {
	e := testEngine(t, &memBoard{})
	old := newSession(nil, "old", true, discardLog(), nil)
	current := newSession(nil, "current", true, discardLog(), nil)
	e.sess, e.haveLast, e.lastSendID = current, true, "new-copy"
	for _, failure := range []struct {
		source *Session
		id     string
	}{{old, "new-copy"}, {current, "old-copy"}} {
		e.allowLocalRetry(failure.source, failure.id)
		if !e.haveLast {
			t.Fatal("old failure reset newer deduplication")
		}
	}
	e.allowLocalRetry(current, "new-copy")
	if e.haveLast {
		t.Fatal("current failed copy stayed suppressed")
	}
}

func TestClipboardAndHistoryFailuresAreBothReported(t *testing.T) {
	for _, failWrite := range []bool{false, true} {
		e := testEngine(t, &memBoard{})
		if failWrite {
			e.Board = &failedBoard{&memBoard{}}
		}
		diskErr := errors.New("disk full")
		e.OnNotice = func(n Notice) error {
			if n.Kind == NoticeRemote {
				return diskErr
			}
			return nil
		}
		if err := e.deliverFrom(nil, mustPNG(t)); !errors.Is(err, diskErr) {
			t.Fatalf("history failure was not reported: %v", err)
		}
	}
}

func TestClipboardRetryStopsWhenConnectionCloses(t *testing.T) {
	e := testEngine(t, &busyBoard{memBoard: &memBoard{}, failures: 100})
	session := newSession(nil, "peer", true, discardLog(), nil)
	e.sess = session
	e.RunOnBoard = func(fn func()) { fn(); close(session.done) }
	start := time.Now()
	if err := e.deliverFrom(session, mustPNG(t)); err == nil || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("closed session kept retrying: %v", err)
	}
}
