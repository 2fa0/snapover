package keepawake

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeBackend struct {
	mu       sync.Mutex
	active   bool
	idleTime time.Duration
	checkErr error
	inputErr error
	events   chan string
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{active: true, events: make(chan string, 100)}
}

func (b *fakeBackend) check() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.checkErr
}

func (b *fakeBackend) interactive() (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.active, nil
}

func (b *fakeBackend) power(on bool) error {
	if on {
		b.events <- "power-on"
	} else {
		b.events <- "power-off"
	}
	return nil
}

func (b *fakeBackend) idle() (time.Duration, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.idleTime, nil
}

func (b *fakeBackend) input() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events <- "input"
	b.idleTime = 0
	return b.inputErr
}

func nextEvent(t *testing.T, b *fakeBackend, want string) {
	t.Helper()
	select {
	case got := <-b.events:
		if got != want {
			t.Fatalf("event = %s, want %s", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", want)
	}
}

func TestIdleInputAndLockLifecycle(t *testing.T) {
	b := newFakeBackend()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := start(ctx, b, time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	nextEvent(t, b, "power-on")
	b.mu.Lock()
	b.idleTime = inputIdle
	b.mu.Unlock()
	nextEvent(t, b, "input")
	b.mu.Lock()
	b.active = false
	b.idleTime = 10 * time.Minute
	b.mu.Unlock()
	nextEvent(t, b, "power-off")
	// A locked session must neither hold a power request nor inject input.
	// Resume with real input so the old idle time cannot cause an injection.
	b.mu.Lock()
	b.active = true
	b.idleTime = 0
	b.mu.Unlock()
	nextEvent(t, b, "power-on")
	cancel()
	c.Stop()
	nextEvent(t, b, "power-off")
	select {
	case got := <-b.events:
		t.Fatalf("unexpected event after stop: %s", got)
	default:
	}
}

func TestPolicyChangeStopsAndReleasesPower(t *testing.T) {
	b := newFakeBackend()
	c, err := start(context.Background(), b, time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	nextEvent(t, b, "power-on")
	blocked := errors.New("policy blocks simulated input")
	b.mu.Lock()
	b.checkErr = blocked
	b.idleTime = inputIdle
	b.mu.Unlock()
	nextEvent(t, b, "power-off")
	c.Stop()
	if !errors.Is(c.Status().Err, blocked) {
		t.Fatalf("policy failure not reported: %+v", c.Status())
	}
}

func TestInputFailureReleasesPower(t *testing.T) {
	b := newFakeBackend()
	denied := errors.New("input denied")
	b.inputErr = denied
	b.idleTime = inputIdle
	c, err := start(context.Background(), b, time.Millisecond, nil)
	if c != nil || !errors.Is(err, denied) {
		t.Fatalf("failed input was accepted: %v", err)
	}
	nextEvent(t, b, "power-on")
	nextEvent(t, b, "input")
	nextEvent(t, b, "power-off")
}

func TestCancelledOrBlockedStartHasNoSideEffects(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		b := newFakeBackend()
		ctx, cancel := context.WithCancel(context.Background())
		want := errors.New("blocked")
		if cancelled {
			cancel()
			want = context.Canceled
		} else {
			b.checkErr = want
		}
		c, err := start(ctx, b, time.Millisecond, nil)
		cancel()
		if c != nil || !errors.Is(err, want) {
			t.Fatalf("start = %v, %v; want %v", c, err, want)
		}
		select {
		case event := <-b.events:
			t.Fatalf("failed start caused %s", event)
		default:
		}
	}
}

func TestLockedStartWaitsForInteractiveDesktop(t *testing.T) {
	b := newFakeBackend()
	b.active = false
	c, err := start(context.Background(), b, time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	if !c.Status().Paused {
		t.Fatal("locked session was not paused")
	}
	select {
	case event := <-b.events:
		t.Fatalf("locked start caused %s", event)
	default:
	}
	b.mu.Lock()
	b.active = true
	b.mu.Unlock()
	nextEvent(t, b, "power-on")
}
