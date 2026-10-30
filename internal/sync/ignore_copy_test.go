package sync

import (
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

func TestIgnoreNextCopyExcludesHistoryAndTransportThenResumes(t *testing.T) {
	board := &memBoard{seq: 10}
	e := testEngine(t, board)
	e.sess = newSession(nil, "peer", true, discardLog(), nil)
	var local []Notice
	e.OnNotice = func(n Notice) error {
		if n.Kind == NoticeLocal {
			local = append(local, n)
		}
		return nil
	}
	if err := e.SetIgnoreNextCopy(true); err != nil {
		t.Fatal(err)
	}
	// An old event waiting in the local queue must not consume the action.
	e.HandleLocal(10, mustText(t, "already copied"))
	if !e.IgnoreNextCopyPending() {
		t.Fatal("queued old copy consumed ignore")
	}
	local = nil
	for len(e.sess.out) > 0 {
		<-e.sess.out
	}
	private := mustText(t, "private")
	e.HandleLocal(11, private)
	e.HandleLocal(11, private)
	if e.IgnoreNextCopyPending() || len(local) != 0 || len(e.sess.out) != 0 {
		t.Fatal("private content entered history or transport")
	}
	if time.Since(e.localActive) > time.Second {
		t.Fatal("private copy did not protect local clipboard")
	}
	// Same bytes in a new generation are a new, public copy.
	e.HandleLocal(12, private)
	if len(local) != 1 || len(e.sess.out) != 1 {
		t.Fatal("next ordinary copy did not resume")
	}
}

func TestIgnoreNextCopyDoesNotConsumeRemoteOrHistoryWrites(t *testing.T) {
	board := &memBoard{seq: 10}
	e := testEngine(t, board)
	if err := e.SetIgnoreNextCopy(true); err != nil {
		t.Fatal(err)
	}
	e.localActive = time.Time{}
	remote := mustText(t, "remote")
	if err := e.deliverFrom(nil, remote); err != nil {
		t.Fatal(err)
	}
	seq, _ := board.Sequence()
	e.HandleLocal(seq, remote)
	if !e.IgnoreNextCopyPending() {
		t.Fatal("remote echo consumed ignore")
	}
	history := mustText(t, "from history")
	history.LocalWrite = true
	e.HandleLocal(seq+1, history)
	if !e.IgnoreNextCopyPending() {
		t.Fatal("history write consumed ignore")
	}
	if err := e.SetIgnoreNextCopy(false); err != nil {
		t.Fatal(err)
	}
	if e.IgnoreNextCopyPending() {
		t.Fatal("cancel did not clear pending ignore")
	}
}

func TestIgnoreNextCopyPrecedesFullCaptureQueueAndClaimsCopyShortcut(t *testing.T) {
	e := testEngine(t, &memBoard{})
	e.localQueue = make(chan localJob, 1)
	e.localQueue <- localJob{}
	e.sess = newSession(nil, "peer", true, discardLog(), nil)
	result, claimed, cancel, err := e.CopyNext(0)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := e.SetIgnoreNextCopy(true); err != nil {
		t.Fatal(err)
	}
	var notices []Notice
	e.OnNotice = func(n Notice) error { notices = append(notices, n); return nil }
	e.ObserveLocal(1, clip.Item{Sensitive: true})
	if e.IgnoreNextCopyPending() || len(e.localQueue) != 1 {
		t.Fatal("full queue prevented ignore")
	}
	select {
	case <-claimed:
	default:
		t.Fatal("copy shortcut left waiting")
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("ignored copy reported success")
		}
	default:
		t.Fatal("copy shortcut has no result")
	}
	if len(notices) != 1 || notices[0].Item.Kind != "" || notices[0].Kind != NoticeStatus {
		t.Fatalf("private payload leaked: %+v", notices)
	}
}
