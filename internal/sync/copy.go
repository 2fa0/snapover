package sync

import (
	"errors"
	"time"
)

const (
	// CopyTimeout is how long to wait for a new clipboard generation.
	CopyTimeout = 2 * time.Second
	// CopyProcessTimeout bounds work after that generation has been observed.
	CopyProcessTimeout = 15 * time.Second
)

type copyRequest struct {
	after     uint64
	until     time.Time
	session   *Session
	result    chan error
	claimed   chan struct{}
	cancelled bool
}

func (e *Engine) SetManualSync(manual bool) {
	e.mu.Lock()
	changed := e.manualSync != manual
	if changed {
		e.haveLast = false
	}
	e.manualSync = manual
	sess := e.sess
	e.mu.Unlock()
	if changed && manual && sess != nil {
		sess.cancelUnsentClipboard(true, "已改为手动同步，未发送")
	}
}

// CopyNext authorizes only a fresh clipboard generation from the next copy.
// The normal watcher owns capture and delivery, including in automatic mode.
// Call cancel if native copying fails, times out, or the application exits.
func (e *Engine) CopyNext(after uint64) (<-chan error, <-chan struct{}, func() bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.paused {
		return nil, nil, nil, errors.New("已暂停同步，请先恢复同步")
	}
	if e.sess == nil {
		return nil, nil, nil, errors.New("没有在线设备，请先连接设备")
	}
	if e.copyRequest != nil {
		return nil, nil, nil, errors.New("正在复制，请稍后再试")
	}
	r := &copyRequest{
		after:   after,
		until:   time.Now().Add(CopyTimeout),
		session: e.sess,
		result:  make(chan error, 1),
		claimed: make(chan struct{}),
	}
	e.copyRequest = r
	// A peer update must not be mistaken for the selected content while waiting.
	e.localActive = time.Now()
	cancel := func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		r.cancelled = true
		if e.copyRequest != r {
			return false
		}
		e.copyRequest = nil
		return true
	}
	return r.result, r.claimed, cancel, nil
}
