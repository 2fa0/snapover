package keepalive

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInputRequiresOptInAndIdleUnlockedRDP(t *testing.T) {
	ready := sessionState{remote: true, connected: true, unlocked: true, idle: idleThreshold}
	for _, name := range []string{"disabled", "console", "disconnected", "locked", "recent input", "held button", "ready"} {
		t.Run(name, func(t *testing.T) {
			s := ready
			on := true
			switch name {
			case "disabled":
				on = false
			case "console":
				s.remote = false
			case "disconnected":
				s.connected = false
			case "locked":
				s.unlocked = false
			case "recent input":
				s.idle = idleThreshold - time.Millisecond
			case "held button":
				s.held = true
			}
			checks, moves := 0, 0
			c := &Controller{
				state: func() (sessionState, error) { checks++; return s, nil },
				move:  func() error { moves++; return nil },
			}
			c.SetEnabled(on)
			if err := c.step(time.Now()); err != nil {
				t.Fatal(err)
			}
			if (moves == 1) != (name == "ready") {
				t.Fatalf("moves = %d", moves)
			}
			if !on && checks != 0 {
				t.Fatal("disabled feature queried Windows")
			}
		})
	}
}

func TestRateLimitAndDisable(t *testing.T) {
	now := time.Now()
	moves := 0
	c := &Controller{
		state: func() (sessionState, error) {
			return sessionState{remote: true, connected: true, unlocked: true, idle: time.Hour}, nil
		},
		move: func() error { moves++; return errors.New("input blocked") },
	}
	c.SetEnabled(true)
	if c.step(now) == nil {
		t.Fatal("input failure was hidden")
	}
	if err := c.step(now.Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if moves != 1 {
		t.Fatal("failed input should still be rate-limited")
	}
	if c.step(now.Add(idleThreshold)) == nil || moves != 2 {
		t.Fatal("expected next attempt after two minutes")
	}
	c.SetEnabled(false)
	if err := c.step(now.Add(time.Hour)); err != nil || moves != 2 {
		t.Fatal("input continued after disabling")
	}
}

func TestSessionQueryFailureDoesNotSendInput(t *testing.T) {
	c := &Controller{state: func() (sessionState, error) { return sessionState{}, errors.New("query failed") }, move: func() error { t.Fatal("input sent without a known session"); return nil }}
	c.SetEnabled(true)
	if c.step(time.Now()) == nil {
		t.Fatal("query failure was hidden")
	}
}

func TestCancelledRunStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New()
	c.SetEnabled(true)
	c.Run(ctx, nil)
	if c.enabled {
		t.Fatal("cancelled runner left input enabled")
	}
}

func TestDisableWaitsForInflightInput(t *testing.T) {
	entered, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c := &Controller{
		state: func() (sessionState, error) {
			return sessionState{remote: true, connected: true, unlocked: true, idle: time.Hour}, nil
		},
		move: func() error { close(entered); <-release; return nil },
	}
	c.SetEnabled(true)
	stepDone := make(chan struct{})
	go func() { _ = c.step(time.Now()); close(stepDone) }()
	<-entered
	go func() { c.SetEnabled(false); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("disable returned while input was still in flight")
	default:
	}
	close(release)
	<-stopped
	<-stepDone
	if c.enabled {
		t.Fatal("disable did not stop the controller")
	}
	if err := c.step(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}
