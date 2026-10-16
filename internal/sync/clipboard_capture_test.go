package sync

import (
	"bytes"
	"image"
	"image/png"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

type copyAfterWriteBoard struct {
	*memBoard
	local     clip.Item
	snapshots int
}

func (b *copyAfterWriteBoard) Write(item clip.Item) error {
	if err := b.memBoard.Write(item); err != nil {
		return err
	}
	return b.memBoard.Write(b.local)
}
func (b *copyAfterWriteBoard) Snapshot() (clip.Item, error) {
	b.snapshots++
	return b.memBoard.Snapshot()
}

func TestRemoteWriteNeverReadsBackOrSuppressesANewLocalCopy(t *testing.T) {
	local := mustText(t, "copied immediately after receiving")
	board := &copyAfterWriteBoard{memBoard: &memBoard{}, local: local}
	e := testEngine(t, board)
	localNotices := 0
	e.OnNotice = func(n Notice) error {
		if n.Kind == NoticeLocal {
			localNotices++
		}
		return nil
	}
	if err := e.deliverFrom(nil, mustText(t, "remote")); err != nil {
		t.Fatal(err)
	}
	seq, _ := board.Sequence()
	e.HandleLocal(seq, local)
	if board.snapshots != 0 || e.Guard.IsEcho(local.Hash) || localNotices != 1 {
		t.Fatalf("new copy was lost: snapshots=%d notices=%d", board.snapshots, localNotices)
	}
}

type preparedRemoteBoard struct {
	*memBoard
	preparingOnUI bool
	inside        bool
	duringPrepare func()
}

func (b *preparedRemoteBoard) PrepareRemoteWrite(item clip.Item) (clip.PreparedWrite, error) {
	b.preparingOnUI = b.inside
	if b.duringPrepare != nil {
		b.duringPrepare()
	}
	return clip.PreparedWrite{Publish: func() error {
		if !b.inside {
			panic("publication escaped the UI callback")
		}
		return b.memBoard.Write(item)
	}}, nil
}

func TestImagePreparationIsOutsideUIAndRechecksNewLocalCopy(t *testing.T) {
	for _, copyDuringPrepare := range []bool{false, true} {
		board := &preparedRemoteBoard{memBoard: &memBoard{}}
		e := testEngine(t, board)
		if copyDuringPrepare {
			board.duringPrepare = func() { _ = board.memBoard.Write(mustText(t, "new local")) }
		}
		e.RunOnBoard = func(fn func()) { board.inside = true; fn(); board.inside = false }
		err := e.deliverFrom(nil, mustPNG(t))
		if board.preparingOnUI {
			t.Fatal("image preparation ran on the UI thread")
		}
		writes := board.Writes()
		if copyDuringPrepare {
			if err != errHistoryOnly || len(writes) != 1 || writes[0].Text != "new local" {
				t.Fatalf("local copy overwritten: %v", err)
			}
		} else if err != nil || len(writes) != 1 || writes[0].Kind != clip.KindImage {
			t.Fatal("prepared image was not published", err)
		}
	}
}

type benchmarkImageBoard struct {
	item     clip.Item
	sequence uint64
}

func (b *benchmarkImageBoard) Sequence() (uint64, error)    { return b.sequence, nil }
func (b *benchmarkImageBoard) Write(item clip.Item) error   { b.item = item; b.sequence++; return nil }
func (b *benchmarkImageBoard) Snapshot() (clip.Item, error) { return clip.FromPNG(b.item.PNG) }

// Measure decode plus delivery, without network, native clipboard or GPU work.
// Snapshot deliberately models the full image readback removed from delivery.
func BenchmarkReceivedLargeImage(b *testing.B) {
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 4000, 10000))); err != nil {
		b.Fatal(err)
	}
	board := &benchmarkImageBoard{}
	e := &Engine{Board: board, Guard: clip.NewEchoGuard(time.Second), Log: discardLog()}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		item, err := clip.FromPNG(raw.Bytes())
		if err != nil {
			b.Fatal(err)
		}
		if err := e.deliverFrom(nil, item); err != nil {
			b.Fatal(err)
		}
	}
}
