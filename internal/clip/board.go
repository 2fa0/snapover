package clip

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/2fa0/snapover/internal/crashlog"
)

var ErrClipboardBusy = errors.New("剪贴板暂时被占用")

// Board is the clipboard as the sync engine sees it. Tests use a fake.
// The MyGo implementation lives in board_mygo.go.
type Board interface {
	// Sequence is a monotonic counter that changes when the clipboard changes.
	// The first observed value is not treated as a new copy.
	Sequence() (uint64, error)
	Snapshot() (Item, error)
	Write(Item) error
}

const (
	// PollEvery is how often Sequence is checked. The check itself is cheap;
	// Snapshot waits until the sequence has settled.
	PollEvery = 50 * time.Millisecond
	// SettleAfter is how long the sequence must stay unchanged before the
	// clipboard is read, stored, or synced. Excel, Figma, and IDEs can update
	// the clipboard many times a second; only the last value is kept.
	SettleAfter = 150 * time.Millisecond
)

// Watch polls Sequence and calls fn only after a change has settled.
// The clipboard contents present at startup are not synced. Intermediate
// updates inside SettleAfter are neither snapshotted nor delivered.
func Watch(ctx context.Context, b Board, every time.Duration, fn func(seq uint64, item Item)) {
	watch(ctx, b, every, SettleAfter, fn)
}

func watch(ctx context.Context, b Board, every, quiet time.Duration, fn func(seq uint64, item Item)) {
	if every <= 0 {
		every = PollEvery
	}
	if quiet < 0 {
		quiet = 0
	}
	var last uint64
	have := false
	var pending uint64
	dirty := false
	tick := time.NewTicker(every)
	defer tick.Stop()
	var settle *time.Timer
	var settleC <-chan time.Time
	stopSettle := func() {
		if settle == nil {
			return
		}
		if !settle.Stop() {
			select {
			case <-settle.C:
			default:
			}
		}
		settle = nil
		settleC = nil
	}
	defer stopSettle()
	arm := func() {
		if quiet == 0 {
			return
		}
		if settle == nil {
			settle = time.NewTimer(quiet)
			settleC = settle.C
			return
		}
		if !settle.Stop() {
			select {
			case <-settle.C:
			default:
			}
		}
		settle.Reset(quiet)
		settleC = settle.C
	}
	emit := func(seq uint64) {
		dirty = false
		stopSettle()
		if own, ok := b.(interface{ OwnsSequence(uint64) bool }); ok && own.OwnsSequence(seq) {
			return
		}
		crashlog.Breadcrumb(fmt.Sprintf("剪贴板稳定 seq=%d", seq))
		item, err := b.Snapshot()
		if err != nil {
			dirty = true
			arm()
			return
		}
		// A slow provider or image decode may outlive another copy. Never
		// attribute data from a different clipboard generation to this event.
		after, err := b.Sequence()
		if err != nil || after != seq {
			dirty = true
			if err == nil {
				last, pending, dirty = after, after, true
			}
			arm()
			return
		}
		fn(seq, item)
	}
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
			if seq == last && !(quiet == 0 && dirty) {
				continue
			}
			last = seq
			pending = seq
			dirty = true
			crashlog.Breadcrumb(fmt.Sprintf("剪贴板变化 seq=%d，等待稳定", seq))
			if quiet == 0 {
				emit(seq)
				continue
			}
			arm()
		case <-settleC:
			if !dirty {
				continue
			}
			seq, err := b.Sequence()
			if err != nil || seq != pending {
				if err == nil {
					last = seq
					pending = seq
				}
				arm()
				continue
			}
			emit(seq)
		}
	}
}
