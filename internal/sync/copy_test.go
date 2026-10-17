package sync

import (
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/wire"
)

func copyTestEngine(t *testing.T) *Engine {
	t.Helper()
	e := testEngine(t, &memBoard{})
	e.sess = newSession(nil, "peer", true, discardLog(), nil)
	return e
}

func TestManualSyncKeepsHistoryAndReceives(t *testing.T) {
	e := copyTestEngine(t)
	e.SetManualSync(true)
	if err := e.deliverFrom(nil, mustText(t, "remote")); err != nil {
		t.Fatal(err)
	}
	if len(e.Board.(*memBoard).Writes()) != 1 {
		t.Fatal("manual mode stopped receiving")
	}
	history := 0
	e.OnNotice = func(n Notice) error {
		if n.Kind == NoticeLocal {
			history++
		}
		return nil
	}
	item := mustText(t, "local")
	e.HandleLocal(1, item)
	if len(e.sess.out) != 0 || history != 1 {
		t.Fatal("ordinary copy sent or history lost")
	}
	e.SetManualSync(false)
	e.HandleLocal(2, item)
	if len(e.sess.out) != 1 {
		t.Fatal("automatic mode failed to send a fresh copy of the same text")
	}
}

func TestCopyShortcutSendsOnceInBothModesAndCanRepeat(t *testing.T) {
	for _, manual := range []bool{false, true} {
		e := copyTestEngine(t)
		e.SetManualSync(manual)
		item := mustText(t, "selected")
		// An explicit copy may intentionally resend previously received contents.
		e.Guard.Mark(item.Hash)
		for seq := uint64(10); seq < 12; seq++ {
			result, cancel, err := e.CopyNext(seq)
			if err != nil {
				t.Fatal(err)
			}
			e.HandleLocal(seq, item)
			if len(e.sess.out) != int(seq-10) {
				t.Fatal("old clipboard was sent")
			}
			select {
			case <-result:
				t.Fatal("old generation completed request")
			default:
			}
			e.HandleLocal(seq+1, item)
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			cancel()
			if len(e.sess.out) != int(seq-9) {
				t.Fatal("explicit copy missing or duplicated")
			}
		}
	}
}

func TestCopyShortcutCancelExpiryAndPolicy(t *testing.T) {
	for _, reason := range []string{"cancel", "expired", "paused", "disconnected", "changed-peer", "sensitive", "unsupported", "image-limit", "text-only"} {
		t.Run(reason, func(t *testing.T) {
			e := copyTestEngine(t)
			e.SetManualSync(true)
			original := e.sess
			result, cancel, err := e.CopyNext(10)
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			item := mustText(t, "new")
			switch reason {
			case "cancel":
				cancel()
			case "expired":
				e.copyRequest.until = time.Now().Add(-time.Second)
			case "paused":
				e.SetPolicy(true, false, 0)
			case "disconnected":
				e.sess = nil
			case "changed-peer":
				e.sess = newSession(nil, "other", true, discardLog(), nil)
			case "sensitive":
				item.Sensitive = true
			case "unsupported":
				item = clip.Item{}
			case "image-limit":
				item = mustPNG(t)
				e.SetPolicy(false, false, 1)
			case "text-only":
				item = mustPNG(t)
				e.SetPolicy(false, true, 0)
			}
			e.HandleLocal(11, item)
			if len(original.out) != 0 || e.sess != nil && len(e.sess.out) != 0 {
				t.Fatal("disallowed copy sent")
			}
			if reason != "cancel" && reason != "expired" {
				select {
				case err := <-result:
					if err == nil {
						t.Fatal("failed copy reported success")
					}
				default:
					t.Fatal("failed copy was not reported")
				}
			}
		})
	}
}

func TestCopyShortcutRequiresConnectionAndRespectsPause(t *testing.T) {
	e := testEngine(t, &memBoard{})
	if _, _, err := e.CopyNext(1); err == nil {
		t.Fatal("offline request accepted")
	}
	e.sess = newSession(nil, "peer", true, discardLog(), nil)
	e.SetPolicy(true, false, 0)
	if _, _, err := e.CopyNext(1); err == nil {
		t.Fatal("paused request accepted")
	}
	e.SetPolicy(false, false, 0)
	_, cancel, err := e.CopyNext(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.CopyNext(1); err == nil {
		t.Fatal("concurrent request accepted")
	}
	cancel()
	if _, cancel, err := e.CopyNext(1); err != nil {
		t.Fatal(err)
	} else {
		cancel()
	}
}

func TestPauseDropsQueuedClipboardBeforeSend(t *testing.T) {
	e := copyTestEngine(t)
	e.sess.onClipboardResult = func(result wire.Ack) {
		if result.State != "complete" {
			e.allowLocalRetry(e.sess, result.ItemID)
		}
	}
	e.HandleLocal(1, mustText(t, "queued-before-pause"))
	if len(e.sess.out) != 1 || e.sess.queuedBytes == 0 {
		t.Fatal("clipboard was not queued")
	}
	e.SetPolicy(true, false, 0)
	if len(e.sess.out) != 0 || e.sess.queuedBytes != 0 {
		t.Fatalf("paused queue still holds clipboard: items=%d bytes=%d", len(e.sess.out), e.sess.queuedBytes)
	}
	if e.haveLast {
		t.Fatal("cancelled copy stayed suppressed")
	}
	if ok, msg := e.admitClipboard(queuedItem{Item: mustText(t, "already-dequeued")}); ok || msg == "" {
		t.Fatal("dequeued clipboard was still sendable while paused")
	}
}

func TestManualModeDropsAutomaticQueue(t *testing.T) {
	e := copyTestEngine(t)
	e.HandleLocal(1, mustText(t, "automatic"))
	if !e.sess.enqueueClipboard(mustText(t, "explicit"), "explicit-id", false, true) {
		t.Fatal("explicit copy was not queued")
	}
	e.SetManualSync(true)
	if len(e.sess.out) != 1 {
		t.Fatalf("queued=%d, want the explicit copy only", len(e.sess.out))
	}
	got := <-e.sess.out
	if got.Text != "explicit" || !got.explicit {
		t.Fatalf("kept %q explicit=%v", got.Text, got.explicit)
	}
}
