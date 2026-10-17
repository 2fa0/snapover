package sync

import (
	"errors"
	"time"
)

const CopyTimeout = 2 * time.Second

type copyRequest struct {
	after   uint64
	until   time.Time
	session *Session
	result  chan error
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
func (e *Engine) CopyNext(after uint64) (<-chan error, func() bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.paused {
		return nil, nil, errors.New("已暂停同步，请先恢复同步")
	}
	if e.sess == nil {
		return nil, nil, errors.New("没有在线设备，请先连接设备")
	}
	if e.copyRequest != nil {
		return nil, nil, errors.New("正在复制，请稍后再试")
	}
	r := &copyRequest{after: after, until: time.Now().Add(CopyTimeout), session: e.sess, result: make(chan error, 1)}
	e.copyRequest = r
	// A peer update must not be mistaken for the selected content while waiting.
	e.localActive = time.Now()
	cancel := func() bool {
		e.mu.Lock()
		if e.copyRequest == r {
			e.copyRequest = nil
			e.mu.Unlock()
			return true
		}
		e.mu.Unlock()
		return false
	}
	return r.result, cancel, nil
}
