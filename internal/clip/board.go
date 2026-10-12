package clip

import (
	"context"
	"fmt"
	"time"

	"github.com/2fa0/snapover/internal/crashlog"
)

// Board is the clipboard as the sync engine sees it. Tests use a fake.
// The MyGo implementation lives in board_mygo.go.
type Board interface {
	// Sequence is a monotonic counter that changes when the clipboard changes.
	// The first observed value is not treated as a new copy.
	Sequence() (uint64, error)
	Snapshot() (Item, error)
	Write(Item) error
}

// Watch polls Sequence and calls fn only after it changes. The clipboard
// contents present at startup are not synced.
func Watch(ctx context.Context, b Board, every time.Duration, fn func(seq uint64, item Item)) {
	if every <= 0 {
		every = 300 * time.Millisecond
	}
	var last uint64
	have := false
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			seq, err := b.Sequence()
			if err != nil {
				continue
			}
			if !have {
				last = seq
				have = true
				continue
			}
			if seq == last {
				continue
			}
			last = seq
			crashlog.Breadcrumb(fmt.Sprintf("剪贴板变化 seq=%d", seq))
			item, err := b.Snapshot()
			if err != nil {
				continue
			}
			fn(seq, item)
		}
	}
}
