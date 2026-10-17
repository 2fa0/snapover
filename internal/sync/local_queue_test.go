package sync

import (
	"context"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

func TestLocalQueueKeepsIntermediateCopy(t *testing.T) {
	board := &memBoard{}
	e := testEngine(t, board)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.StartLocalQueue(ctx)

	started := make(chan struct{})
	release := make(chan struct{})
	got := make(chan string, 4)
	e.OnNotice = func(n Notice) error {
		if n.Kind != NoticeLocal {
			return nil
		}
		if n.Item.Text == "A" {
			close(started)
			<-release
		}
		got <- n.Item.Text
		return nil
	}
	go clip.Watch(ctx, board, 5*time.Millisecond, func(seq uint64, item clip.Item) {
		e.ObserveLocal(seq, item)
	})
	time.Sleep(30 * time.Millisecond)
	if err := board.Write(mustText(t, "A")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first copy was not observed")
	}
	if err := board.Write(mustText(t, "B")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := board.Write(mustText(t, "C")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	close(release)

	deadline := time.After(time.Second)
	seen := map[string]bool{}
	for len(seen) < 3 {
		select {
		case text := <-got:
			seen[text] = true
		case <-deadline:
			t.Fatalf("intermediate copy lost: %+v", seen)
		}
	}
}
