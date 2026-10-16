package clip

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
)

func TestRemoteMarkerRequiresSameClipboardGeneration(t *testing.T) {
	oldRead, oldSeq := readClipboardData, clipboardSequence
	t.Cleanup(func() { readClipboardData, clipboardSequence = oldRead, oldSeq })
	for _, tc := range []struct {
		marker []byte
		seq    uint64
		err    error
		own    bool
	}{
		{remoteMarker, 2, nil, true},
		{append(append([]byte(nil), remoteMarker...), 0, 0), 2, nil, true},
		{remoteMarker, 3, nil, false},
		{[]byte("another process"), 2, nil, false},
		{nil, 2, errors.New("format missing"), false},
	} {
		readClipboardData = func(f transfer.Format) ([]byte, error) {
			if f != remoteFormat {
				t.Fatal(f)
			}
			return tc.marker, tc.err
		}
		clipboardSequence = func() (uint64, error) { return tc.seq, nil }
		if got := (MyGoBoard{}).OwnsSequence(2); got != tc.own {
			t.Fatalf("marker=%q seq=%d own=%v", tc.marker, tc.seq, got)
		}
	}
}

type markedBoard struct {
	*fakeBoard
	ready            chan struct{}
	checked          chan struct{}
	own              bool
	changeDuringRead bool
}

func (b *markedBoard) Sequence() (uint64, error) {
	seq, err := b.fakeBoard.Sequence()
	select {
	case b.ready <- struct{}{}:
	default:
	}
	return seq, err
}
func (b *markedBoard) OwnsSequence(uint64) bool {
	b.mu.Lock()
	own := b.own
	b.mu.Unlock()
	select {
	case b.checked <- struct{}{}:
	default:
	}
	return own
}
func (b *markedBoard) Snapshot() (Item, error) {
	item, err := b.fakeBoard.Snapshot()
	if b.changeDuringRead {
		b.changeDuringRead = false
		b.Set(Item{Kind: KindText, Text: "latest"})
	}
	return item, err
}
func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not advance")
	}
}
func TestWatchSkipsRemoteImageBeforeSnapshot(t *testing.T) {
	b := &markedBoard{fakeBoard: &fakeBoard{seq: 1}, ready: make(chan struct{}, 1), checked: make(chan struct{}, 2), own: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	got := make(chan Item, 2)
	go func() {
		defer close(done)
		watch(ctx, b, time.Millisecond, 5*time.Millisecond, func(_ uint64, it Item) { got <- it })
	}()
	waitSignal(t, b.ready)
	b.Set(Item{Kind: KindImage, PNG: []byte("not decoded")})
	waitSignal(t, b.checked)
	b.mu.Lock()
	if b.snaps != 0 {
		t.Fatal("remote image was decoded")
	}
	b.own = false
	b.mu.Unlock()
	b.Set(Item{Kind: KindText, Text: "local"})
	select {
	case item := <-got:
		if item.Text != "local" {
			t.Fatal(item)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("local copy skipped")
	}
	cancel()
	<-done
}
func TestWatchDiscardsDataWhenClipboardChangesDuringRead(t *testing.T) {
	b := &markedBoard{fakeBoard: &fakeBoard{seq: 1}, ready: make(chan struct{}, 1), checked: make(chan struct{}, 2), changeDuringRead: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan Item, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watch(ctx, b, time.Millisecond, 5*time.Millisecond, func(_ uint64, it Item) { got <- it })
	}()
	waitSignal(t, b.ready)
	b.Set(Item{Kind: KindText, Text: "stale"})
	select {
	case item := <-got:
		if item.Text != "latest" {
			t.Fatal("stale generation delivered", item)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("new copy lost")
	}
	cancel()
	<-done
}
