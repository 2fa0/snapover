package control

import (
	"context"
	"errors"
	"sync"
	"time"
)

const idleTimeout = 15 * time.Minute

var errNoProgress = errors.New("长时间没有传输进展，已停止发送；请检查连接和磁盘后重试")

// Only increasing byte counts extend the deadline, not repeated status events.
type progressDeadline struct {
	mu     sync.Mutex
	timer  *time.Timer
	cancel context.CancelCauseFunc
	idle   time.Duration
	last   time.Time
	done   int64
	closed bool
}

func watchProgress(parent context.Context, idle time.Duration) (context.Context, *progressDeadline) {
	ctx, cancel := context.WithCancelCause(parent)
	p := &progressDeadline{cancel: cancel, idle: idle, last: time.Now()}
	p.timer = time.AfterFunc(idle, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.closed {
			return
		}
		if remaining := p.idle - time.Since(p.last); remaining > 0 {
			p.timer.Reset(remaining)
			return
		}
		p.cancel(errNoProgress)
	})
	return ctx, p
}

func (p *progressDeadline) advance(done int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed && done > p.done {
		p.done = done
		p.last = time.Now()
		p.timer.Reset(p.idle)
	}
}

func (p *progressDeadline) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.timer.Stop()
	p.cancel(nil)
}
