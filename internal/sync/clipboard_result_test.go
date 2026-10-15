package sync

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/wire"
)

func waitClipboardResult(t *testing.T, notices <-chan Notice) Notice {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case n := <-notices:
			if n.Kind == NoticeSync {
				return n
			}
		case <-timer.C:
			t.Fatal("no automatic clipboard confirmation")
			return Notice{}
		}
	}
}

func TestAutomaticCopyReportsActualResultWithoutRecordingTwice(t *testing.T) {
	for _, reason := range []string{"complete", "paused", "write-failed", "history", "history-failed", "history-over-limit"} {
		t.Run(reason, func(t *testing.T) {
			a, b := newPair(t)
			notices := make(chan Notice, 16)
			a.mu.Lock()
			a.OnNotice = func(n Notice) error { notices <- n; return nil }
			a.mu.Unlock()
			store, err := history.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if reason == "history-over-limit" {
				if err := store.SetCapacity(1); err != nil {
					t.Fatal(err)
				}
			}
			b.mu.Lock()
			b.OnNotice = func(n Notice) error {
				if n.Kind == NoticeRemote {
					if reason == "history-failed" {
						return errors.New("disk full")
					}
					_, err := store.Add(history.DirRemote, n.PeerName, n.Item)
					return err
				}
				return nil
			}
			switch reason {
			case "paused":
				b.paused = true
			case "write-failed":
				b.Board = &failedBoard{b.Board.(*memBoard)}
			case "history", "history-failed", "history-over-limit":
				b.localActive = time.Now()
			}
			b.mu.Unlock()
			item := mustText(t, "new automatic copy")
			a.HandleLocal(1, item)
			n := waitClipboardResult(t, notices)
			want := "error"
			if reason == "complete" || reason == "history" {
				want = reason
			}
			if n.ClipboardState != want {
				t.Fatalf("state=%s message=%s", n.ClipboardState, n.Message)
			}
			if want != "complete" && n.Message == "" {
				t.Fatal("missing failure reason")
			}
			if n.Item.Kind != "" {
				t.Fatal("confirmation would duplicate history")
			}
			if reason == "history" {
				if len(b.Board.(*memBoard).Writes()) != 0 {
					t.Fatal("conflict overwrote clipboard")
				}
				entries := store.List()
				if len(entries) != 1 || entries[0].Text != item.Text || entries[0].Dir != history.DirRemote || entries[0].Peer != a.Name {
					t.Fatalf("missing conflict history: %+v", entries)
				}
			}
			a.mu.Lock()
			session := a.sess
			a.mu.Unlock()
			session.mu.Lock()
			pending := len(session.clipboardPending)
			session.mu.Unlock()
			if pending != 0 {
				t.Fatal("confirmed copy retained pending state")
			}
		})
	}
}

func TestClipboardConfirmationTimeoutDisconnectAndBounds(t *testing.T) {
	s := newSession(nil, "peer", false, discardLog(), nil)
	s.clipboardResultSupported = true
	var got []wire.Ack
	s.onClipboardResult = func(n wire.Ack) { got = append(got, n) }
	for range queueSize + 1 {
		if !s.enqueue(mustText(t, "small")) {
			t.Fatal("valid copy rejected")
		}
		s.releaseQueued(<-s.out)
	}
	if s.enqueue(mustText(t, "overflow")) {
		t.Fatal("unconfirmed copies grew without bound")
	}
	s.expireClipboard(time.Now().Add(clipboardWait+time.Second), false)
	if len(got) != queueSize+1 || len(s.clipboardPending) != 0 || !strings.Contains(got[0].Message, "超时") {
		t.Fatal("timeout did not report and release pending copies")
	}
	if !s.enqueue(mustText(t, "after timeout")) {
		t.Fatal("timeout kept queue blocked")
	}
	id := (<-s.out).id
	s.expireClipboard(time.Now(), true)
	if len(got) != queueSize+2 || !strings.Contains(got[len(got)-1].Message, "断开") {
		t.Fatal("disconnect silently lost pending copy")
	}
	s.completeClipboard(wire.Ack{ItemID: id, State: "complete"})
	if len(got) != queueSize+2 {
		t.Fatal("late confirmation was reported twice")
	}
}

func TestSimultaneousLocalCopiesKeepBothHistories(t *testing.T) {
	a, b := newPair(t)
	notices := []chan Notice{make(chan Notice, 16), make(chan Notice, 16)}
	stores := make([]*history.Store, 2)
	items := []string{"from left", "from right"}
	for i, engine := range []*Engine{a, b} {
		store, err := history.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = store
		item := mustText(t, items[i])
		if err := engine.Board.Write(item); err != nil {
			t.Fatal(err)
		}
		engine.mu.Lock()
		engine.localActive = time.Now()
		engine.OnNotice = func(n Notice) error {
			if n.Kind == NoticeLocal || n.Kind == NoticeRemote {
				dir := history.DirLocal
				if n.Kind == NoticeRemote {
					dir = history.DirRemote
				}
				if _, err := store.Add(dir, n.PeerName, n.Item); err != nil {
					return err
				}
			}
			notices[i] <- n
			return nil
		}
		engine.mu.Unlock()
	}
	for i, engine := range []*Engine{a, b} {
		go engine.HandleLocal(1, mustText(t, items[i]))
	}
	for i, engine := range []*Engine{a, b} {
		if n := waitClipboardResult(t, notices[i]); n.ClipboardState != "history" {
			t.Fatalf("conflict result: %+v", n)
		}
		if got := stores[i].List(); len(got) != 2 || got[0].Dir == got[1].Dir {
			t.Fatalf("lost one copy: %+v", got)
		}
		writes := engine.Board.(*memBoard).Writes()
		if len(writes) != 1 || writes[0].Text != items[i] {
			t.Fatal("simultaneous copy overwrote local clipboard")
		}
	}
}

func TestClipboardDeadlineRunsWithoutNetworkWriterAndSkipsExpiredQueue(t *testing.T) {
	s := newSession(nil, "peer", false, discardLog(), nil)
	s.clipboardResultSupported = true
	results := make(chan wire.Ack, 2)
	s.onClipboardResult = func(result wire.Ack) { results <- result }
	if !s.enqueue(mustText(t, "expired")) {
		t.Fatal("enqueue failed")
	}
	item := <-s.out
	s.mu.Lock()
	s.clipboardPending[item.id] = time.Now().Add(20 * time.Millisecond)
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.watchClipboard(); close(done) }()
	defer func() { close(s.done); <-done }()
	select {
	case result := <-results:
		if result.ItemID != item.id || !strings.Contains(result.Message, "超时") {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("deadline depended on network writer")
	}
	if s.clipboardSendable(item, time.Now()) {
		t.Fatal("expired automatic copy was still sendable")
	}
	if !s.clipboardSendable(queuedItem{id: "explicit-cli"}, time.Now()) {
		t.Fatal("automatic expiry cancelled explicit request")
	}
	if !s.enqueue(mustText(t, "fresh")) {
		t.Fatal("timeout retained pending budget")
	}
	fresh := <-s.out
	if !s.clipboardSendable(fresh, time.Now()) {
		t.Fatal("fresh queued copy was dropped")
	}
}
