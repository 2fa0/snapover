package keepalive

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticsVerifyRDPInputSeparately(t *testing.T) {
	for _, outcome := range []string{"reset", "not reset", "unavailable"} {
		t.Run(outcome, func(t *testing.T) {
			var logs bytes.Buffer
			s := sessionState{remote: true, connected: true, unlocked: true, idle: idleThreshold,
				sessionID: 3, rdpLastInput: 100, rdpIdle: time.Hour}
			moves := 0
			c := &Controller{
				state: func() (sessionState, error) { return s, nil },
				move:  func() error { moves++; return nil },
				log:   slog.New(slog.NewTextHandler(&logs, nil)),
			}
			if outcome == "unavailable" {
				s.rdpError = errors.New("WTS query unavailable")
			}
			c.SetEnabled(true)
			now := time.Now()
			if err := c.step(now); err != nil {
				t.Fatal(err)
			}
			if moves != 1 || !strings.Contains(logs.String(), "远程会话保活已发送鼠标输入") {
				t.Fatal("input attempt was blocked or not logged")
			}
			// Local synthetic input can update GetLastInputInfo even if WTS's
			// session input time does not advance. Verify during the cooldown.
			s.idle = 10 * time.Second
			if outcome == "reset" {
				s.rdpLastInput++
				s.rdpIdle = 10 * time.Second
			}
			if err := c.step(now.Add(10 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if moves != 1 || !strings.Contains(logs.String(), "远程会话保活效果") {
				t.Fatal("cooldown should verify the result without sending more input")
			}
			if strings.Contains(logs.String(), "未观察到会话输入时间更新") != (outcome == "not reset") {
				t.Fatalf("incorrect RDP result: %s", logs.String())
			}
			logs.Reset()
			if err := c.step(now.Add(20 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if logs.Len() != 0 {
				t.Fatalf("unchanged state repeated diagnostics: %s", logs.String())
			}
		})
	}
}

func TestDiagnosticsReportWhyInputIsSkipped(t *testing.T) {
	var logs bytes.Buffer
	s := sessionState{sessionID: 3}
	c := &Controller{
		state: func() (sessionState, error) { return s, nil },
		move:  func() error { t.Fatal("input sent while session was not ready"); return nil },
		log:   slog.New(slog.NewTextHandler(&logs, nil)),
	}
	c.SetEnabled(true)
	for _, reason := range []string{"非 RDP 会话", "会话未连接", "锁屏或安全桌面", "鼠标按钮或修饰键按住", "尚未空闲两分钟", "已关闭"} {
		logs.Reset()
		switch reason {
		case "会话未连接":
			s.remote = true
		case "锁屏或安全桌面":
			s.connected = true
		case "鼠标按钮或修饰键按住":
			s.unlocked, s.held = true, true
		case "尚未空闲两分钟":
			s.held = false
		case "已关闭":
			c.SetEnabled(false)
		}
		if err := c.step(time.Now()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(logs.String(), reason) {
			t.Fatalf("missing skip reason %q: %s", reason, logs.String())
		}
	}
}

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

func TestDiagnosticsLogSessionAndQueryChangesWithoutRepeatingIdle(t *testing.T) {
	var logs bytes.Buffer
	s := sessionState{remote: true, connected: true, unlocked: true, sessionID: 3, protocol: 2, desktop: "Default"}
	c := &Controller{state: func() (sessionState, error) { return s, nil }, move: func() error { t.Fatal("unexpected input"); return nil }, log: slog.New(slog.NewTextHandler(&logs, nil))}
	c.SetEnabled(true)
	if err := c.step(time.Now()); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	s.idle = time.Second
	if err := c.step(time.Now()); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Fatal("idle counters repeated the same diagnostic")
	}
	for _, change := range []func(){func() { s.sessionID++ }, func() { s.rdpError = errors.New("WTS unavailable") }, func() { s.rdpError = nil }} {
		logs.Reset()
		change()
		if err := c.step(time.Now()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(logs.String(), "远程会话保活状态") {
			t.Fatal("session/query change was hidden")
		}
	}
}
