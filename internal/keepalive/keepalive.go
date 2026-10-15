// Package keepalive optionally generates input in an idle, unlocked RDP session.
package keepalive

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const idleThreshold = 2 * time.Minute

type sessionState struct {
	remote, connected, unlocked, held bool
	idle                              time.Duration
	sessionID                         uint32
	protocol                          uint32
	desktop                           string
	rdpLastInput                      int64
	rdpIdle                           time.Duration
	rdpError                          error
}

// Exclude continuously changing idle counters from deduplication.
type diagnosticStatus struct {
	reason              string
	sessionID, protocol uint32
	desktop, rdpError   string
}

type Controller struct {
	mu          sync.Mutex
	enabled     bool
	lastAttempt time.Time
	state       func() (sessionState, error)
	move        func() error
	log         *slog.Logger
	lastStatus  diagnosticStatus
	verify      *sessionState
}

func New() *Controller {
	return &Controller{state: currentState, move: moveMouse}
}

// SetEnabled waits for any in-flight input so no input is sent after disabling.
func (c *Controller) SetEnabled(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enabled != on {
		c.lastAttempt = time.Time{}
		c.verify = nil
		c.lastStatus = diagnosticStatus{}
	}
	c.enabled = on
}

func (c *Controller) Run(ctx context.Context, log *slog.Logger) {
	c.mu.Lock()
	c.log = log
	c.mu.Unlock()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var lastError string
	for {
		select {
		case <-ctx.Done():
			c.SetEnabled(false)
			return
		case now := <-ticker.C:
			if ctx.Err() != nil {
				continue
			}
			err := c.step(now)
			if err == nil {
				lastError = ""
			} else if err.Error() != lastError {
				lastError = err.Error()
				if log != nil {
					log.Warn("保持远程会话活跃失败", "err", err)
				}
			}
		}
	}
}

func (c *Controller) step(now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		c.report("已关闭", sessionState{})
		return nil
	}
	s, err := c.state()
	if err != nil {
		return err
	}
	if before := c.verify; before != nil {
		c.verify = nil
		// Check on the next tick: SendInput returning successfully only confirms
		// insertion into the input stream, not an RDP idle timer reset.
		if s.remote && s.connected && s.unlocked && s.sessionID == before.sessionID && c.log != nil {
			c.log.Info("远程会话保活效果", "input_idle", s.idle, "input_idle_before", before.idle,
				"rdp_idle", s.rdpIdle, "rdp_idle_before", before.rdpIdle,
				"rdp_input_advanced", s.rdpLastInput > before.rdpLastInput,
				"rdp_err", s.rdpError, "rdp_err_before", before.rdpError)
			if before.rdpError == nil && s.rdpError == nil && before.rdpLastInput > 0 && s.rdpLastInput <= before.rdpLastInput {
				c.log.Warn("远程会话保活后未观察到会话输入时间更新", "rdp_idle", s.rdpIdle)
			}
		}
	}
	var reason string
	switch {
	case !s.remote:
		reason = "非 RDP 会话"
	case !s.connected:
		reason = "会话未连接"
	case !s.unlocked:
		reason = "锁屏或安全桌面"
	case s.held:
		reason = "鼠标按钮或修饰键按住"
	case s.idle < idleThreshold:
		reason = "尚未空闲两分钟"
	}
	c.report(reason, s)
	if reason != "" || (!c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < idleThreshold) {
		return nil
	}
	// Also rate-limit attempts when synthetic input does not reset idle time.
	c.lastAttempt = now
	if err := c.move(); err != nil {
		return err
	}
	c.verify = &s
	if c.log != nil {
		c.log.Info("远程会话保活已发送鼠标输入", "session_id", s.sessionID, "input_idle", s.idle, "rdp_idle", s.rdpIdle, "rdp_err", s.rdpError)
	}
	return nil
}

func (c *Controller) report(reason string, s sessionState) {
	if reason == "" {
		reason = "允许发送输入"
	}
	status := diagnosticStatus{reason: reason, sessionID: s.sessionID, protocol: s.protocol, desktop: s.desktop}
	if s.rdpError != nil {
		status.rdpError = s.rdpError.Error()
	}
	if status == c.lastStatus {
		return
	}
	c.lastStatus = status
	if c.log != nil {
		c.log.Info("远程会话保活状态", "reason", reason, "session_id", s.sessionID, "protocol", s.protocol,
			"connected", s.connected, "desktop", s.desktop, "input_idle", s.idle, "rdp_idle", s.rdpIdle, "rdp_err", s.rdpError)
	}
}
