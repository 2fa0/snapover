// Package keepawake manages an opt-in request to keep the interactive session awake.
package keepawake

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"time"
)

const inputIdle = time.Minute

type backend interface {
	check() error
	interactive() (bool, error)
	power(bool) error
	idle() (time.Duration, error)
	input() error
}

type Status struct {
	Paused bool
	Err    error
}

type Controller struct {
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	status Status
}

func Start(ctx context.Context, log *slog.Logger) (*Controller, error) {
	b, err := platformBackend()
	if err != nil {
		return nil, err
	}
	return start(ctx, b, 5*time.Second, log)
}

func start(ctx context.Context, b backend, interval time.Duration, log *slog.Logger) (*Controller, error) {
	ctx, cancel := context.WithCancel(ctx)
	c := &Controller{cancel: cancel, done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		defer cancel()
		// Execution state belongs to an OS thread. Let this thread terminate
		// with the goroutine, even if clearing its request fails on exit.
		runtime.LockOSThread()
		defer close(c.done)
		held := false
		var lastInput time.Time
		defer func() {
			if held {
				if err := b.power(false); err != nil {
					c.setStatus(Status{Err: err}, log)
				}
			}
		}()
		step := func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			active, err := b.interactive()
			if err != nil {
				return err
			}
			if active != held {
				if err := b.power(active); err != nil {
					return err
				}
				held = active
			}
			c.setStatus(Status{Paused: !active}, log)
			if !active {
				return nil
			}
			idle, err := b.idle()
			if err != nil || idle < inputIdle || time.Since(lastInput) < inputIdle {
				return err
			}
			// Recheck before each injection in case policy changed while running.
			if err := b.check(); err != nil {
				return err
			}
			if err := b.input(); err != nil {
				return err
			}
			lastInput = time.Now()
			return nil
		}
		err := ctx.Err()
		if err == nil {
			err = b.check()
		}
		if err == nil {
			err = step()
		}
		ready <- err
		if err != nil {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := step(); err != nil {
					if ctx.Err() == nil {
						c.setStatus(Status{Err: err}, log)
					}
					return
				}
			}
		}
	}()
	if err := <-ready; err != nil {
		c.Stop()
		return nil, err
	}
	return c, nil
}

// Stop waits for the worker to release its power request. It is safe to repeat.
func (c *Controller) Stop() {
	c.cancel()
	<-c.done
}

func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Controller) setStatus(s Status, log *slog.Logger) {
	c.mu.Lock()
	c.status = s
	c.mu.Unlock()
	if s.Err != nil && log != nil {
		log.Warn("防止自动锁屏已停止", "err", s.Err)
	}
}
