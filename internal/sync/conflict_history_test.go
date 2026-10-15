package sync

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/history"
)

func TestConflictStillHonorsPauseAndTextLimit(t *testing.T) {
	e := testEngine(t, &memBoard{})
	e.localActive = time.Now()
	var recorded int
	e.OnNotice = func(n Notice) error {
		if n.Kind == NoticeRemote {
			recorded++
		}
		return nil
	}
	e.SetPolicy(true, false, 0)
	if err := e.deliverFrom(nil, mustText(t, "paused")); err == nil || errors.Is(err, errHistoryOnly) {
		t.Fatal("pause was treated as conflict")
	}
	e.SetPolicy(false, false, 0)
	e.SetTextLimit(1)
	if err := e.deliverFrom(nil, mustText(t, "too large")); err == nil || errors.Is(err, errHistoryOnly) {
		t.Fatal("oversize text was treated as conflict")
	}
	if recorded != 0 {
		t.Fatal("pause or limit stored remote content")
	}
	image := mustPNG(t)
	if err := e.deliverFrom(nil, image); !errors.Is(err, errHistoryOnly) || recorded != 1 {
		t.Fatalf("image conflict not saved: %v %d", err, recorded)
	}
	if e.Guard.IsEcho(image.Hash) {
		t.Fatal("history-only receive marked unwritten content as echo")
	}
}

func TestCLIConflictSavesHistoryWithoutReportingClipboardSuccess(t *testing.T) {
	a, b := newPair(t)
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.localActive = time.Now()
	b.OnNotice = func(n Notice) error {
		if n.Kind == NoticeRemote {
			_, err := store.Add(history.DirRemote, n.PeerName, n.Item)
			return err
		}
		return nil
	}
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	item := mustText(t, "keep as history")
	err = a.SendClipboard(ctx, b.LocalID, item)
	if err == nil || !strings.Contains(err.Error(), "已存入对端历史") {
		t.Fatalf("CLI claimed clipboard success: %v", err)
	}
	if len(store.List()) != 1 || len(b.Board.(*memBoard).Writes()) != 0 {
		t.Fatal("conflict lost history or overwrote clipboard")
	}
}
