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
}

type Controller struct {
	mu          sync.Mutex
	enabled     bool
	lastAttempt time.Time
	state       func() (sessionState, error)
	move        func() error
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
	}
	c.enabled = on
}

func (c *Controller) Run(ctx context.Context, log *slog.Logger) {
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
	if !c.enabled || (!c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < idleThreshold) {
		return nil
	}
	s, err := c.state()
	if err != nil {
		return err
	}
	if !s.remote || !s.connected || !s.unlocked || s.held || s.idle < idleThreshold {
		return nil
	}
	// Also rate-limit attempts when synthetic input does not reset idle time.
	c.lastAttempt = now
	return c.move()
}
