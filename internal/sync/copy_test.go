package sync

import (
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
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
