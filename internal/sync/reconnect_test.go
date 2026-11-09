package sync

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/lan"
)

func reconnectEngine(t *testing.T) *Engine {
	t.Helper()
	e := testEngine(t, &memBoard{})
	if _, err := e.Peers.Allow("selected", "aabb", "", "", ""); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestReconnectTriesAllPathsBeforeBackoffAndCapsDelay(t *testing.T) {
	e := reconnectEngine(t)
	paths := []lan.Endpoint{
		{Addr: "192.168.1.10:47821", Route: lan.RouteLAN},
		{Addr: "192.168.2.10:47821", Route: lan.RouteLAN},
		{Addr: "100.65.1.10:47821", Route: lan.RouteTailscale},
	}
	target := NewDialTarget("selected", 1, append(paths, paths[0]))
	var attempts []lan.Endpoint
	var delays []time.Duration
	reconnect(context.Background(), target, e, func(_ context.Context, ep lan.Endpoint, id string, _ *Engine) error {
		if id != "selected" {
			t.Fatal("target identity changed")
		}
		attempts = append(attempts, ep)
		return errors.New("offline")
	}, func(_ context.Context, d time.Duration) bool {
		delays = append(delays, d)
		return len(delays) < 8
	})
	wantDelays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second}
	if !reflect.DeepEqual(delays, wantDelays) {
		t.Fatalf("backoff: %v", delays)
	}
	for round := range 8 {
		if !reflect.DeepEqual(attempts[round*3:(round+1)*3], paths) {
			t.Fatalf("round %d: %v", round, attempts)
		}
	}
}

func TestExplicitEndpointIsPreferredOnlyForFirstRound(t *testing.T) {
	e := reconnectEngine(t)
	local := lan.Endpoint{Addr: "192.168.1.20:47821", Route: lan.RouteLAN}
	tail := lan.Endpoint{Addr: "100.65.1.20:47821", Route: lan.RouteTailscale}
	target := NewDialTarget("selected", 0, []lan.Endpoint{local, tail})
	target.Prefer(tail)
	var attempts []lan.Endpoint
	rounds := 0
	reconnect(context.Background(), target, e, func(_ context.Context, ep lan.Endpoint, _ string, _ *Engine) error {
		attempts = append(attempts, ep)
		return errors.New("offline")
	}, func(context.Context, time.Duration) bool {
		rounds++
		return rounds < 2
	})
	if !reflect.DeepEqual(attempts, []lan.Endpoint{tail, local, local, tail}) {
		t.Fatalf("explicit priority leaked into automatic retries: %+v", attempts)
	}
}

func TestReconnectQuarantinesBadIdentitiesAndStopsOnPairRejection(t *testing.T) {
	for _, rejection := range []error{ErrWrongPeer, ErrFingerprintMismatch, ErrPairingStopped} {
		t.Run(rejection.Error(), func(t *testing.T) {
			e := reconnectEngine(t)
			bad := lan.Endpoint{Addr: "192.168.1.10:47821", Route: lan.RouteLAN}
			good := lan.Endpoint{Addr: "100.65.1.10:47821", Route: lan.RouteTailscale}
			target := NewDialTarget("selected", 0, []lan.Endpoint{bad, good})
			calls, rounds := map[lan.Endpoint]int{}, 0
			reconnect(context.Background(), target, e, func(_ context.Context, ep lan.Endpoint, _ string, _ *Engine) error {
				calls[ep]++
				if ep == bad {
					return rejection
				}
				return errors.New("offline")
			}, func(context.Context, time.Duration) bool {
				rounds++
				target.Update([]lan.Endpoint{bad, good})
				return rounds < 3
			})
			if calls[bad] != 1 {
				t.Fatalf("bad address retried: %v", calls)
			}
			if rejection == ErrPairingStopped {
				if rounds != 0 || calls[good] != 0 {
					t.Fatal("pair rejection restarted pairing")
				}
			} else if calls[good] != 3 {
				t.Fatalf("other path was not tried: %v", calls)
			}
		})
	}
}

func TestDiscoveryUpdatesCandidatesWithoutCancelingCurrentAttempt(t *testing.T) {
	e := reconnectEngine(t)
	old := lan.Endpoint{Addr: "192.168.1.10:47821", Route: lan.RouteLAN}
	newLAN := lan.Endpoint{Addr: "192.168.1.20:47821", Route: lan.RouteLAN}
	tail := lan.Endpoint{Addr: "100.65.1.10:47821", Route: lan.RouteTailscale}
	target := NewDialTarget("selected", 0, []lan.Endpoint{old, tail})
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var attempts []lan.Endpoint
	rounds := 0
	go func() {
		defer close(done)
		reconnect(context.Background(), target, e, func(ctx context.Context, ep lan.Endpoint, _ string, _ *Engine) error {
			attempts = append(attempts, ep)
			if ep == old {
				close(entered)
				<-release
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
			return errors.New("offline")
		}, func(context.Context, time.Duration) bool { rounds++; return rounds < 2 })
	}()
	<-entered
	target.Update([]lan.Endpoint{newLAN, tail})
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconnect stalled")
	}
	if !reflect.DeepEqual(attempts, []lan.Endpoint{old, tail, newLAN, tail}) {
		t.Fatalf("discovery interrupted/reordered current round: %v", attempts)
	}
}

func TestReconnectPreservesLiveSessionAndRevokedTarget(t *testing.T) {
	e := reconnectEngine(t)
	e.sess = newSession(nil, "selected", true, discardLog(), nil)
	target := NewDialTarget("selected", 0, []lan.Endpoint{{Addr: "127.0.0.1:47821", Route: lan.RouteLAN}})
	called := false
	attempt := func(context.Context, lan.Endpoint, string, *Engine) error { called = true; return nil }
	reconnect(context.Background(), target, e, attempt, func(_ context.Context, d time.Duration) bool {
		if d != 500*time.Millisecond {
			t.Fatalf("connected polling: %s", d)
		}
		return false
	})
	if called {
		t.Fatal("live connection was replaced")
	}
	e.sess = nil
	if err := e.Peers.Forget("selected"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reconnect(ctx, target, e, attempt, wait)
	if called {
		t.Fatal("canceled controller dialed")
	}
}

func TestCancelDialInterruptsTLSHandshake(t *testing.T) {
	left, _ := newIdentities(t)
	e := engineFor(t, left, &memBoard{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() { conn, _ := ln.Accept(); accepted <- conn }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- dialEndpoint(ctx, lan.Endpoint{Addr: ln.Addr().String(), Route: lan.RouteLAN}, "", e) }()
	select {
	case conn := <-accepted:
		if conn == nil {
			t.Fatal("accept failed")
		}
		defer conn.Close()
	case <-time.After(time.Second):
		t.Fatal("dial did not start")
	}
	e.CancelDial(cancel)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled handshake succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for TLS timeout")
	}
	if len(e.Peers.List()) != 0 || e.Connected() {
		t.Fatal("canceled handshake became trusted")
	}
}

func TestReconnectTriesOtherTrustedPeerBeforeBackoff(t *testing.T) {
	e := reconnectEngine(t)
	if _, err := e.Peers.Allow("other", "ccdd", "", "", ""); err != nil {
		t.Fatal(err)
	}
	selected := lan.Endpoint{Addr: "192.168.1.10:47821", Route: lan.RouteLAN}
	other := lan.Endpoint{Addr: "192.168.1.30:47821", Route: lan.RouteLAN}
	target := NewDialTarget("selected", 0, nil)
	target.UpdateCandidates([]DialCandidate{
		{PeerID: "selected", Endpoint: selected},
		{PeerID: "other", Endpoint: other},
	})
	var attempts []string
	rounds := 0
	reconnect(context.Background(), target, e, func(_ context.Context, ep lan.Endpoint, id string, _ *Engine) error {
		attempts = append(attempts, id+" "+ep.Addr)
		return errors.New("offline")
	}, func(context.Context, time.Duration) bool {
		rounds++
		return rounds < 2
	})
	want := []string{
		"selected 192.168.1.10:47821",
		"other 192.168.1.30:47821",
		"selected 192.168.1.10:47821",
		"other 192.168.1.30:47821",
	}
	if !reflect.DeepEqual(attempts, want) {
		t.Fatalf("attempts: %v", attempts)
	}
}

func TestReconnectContinuesOtherPeerAfterSelectedIsRevoked(t *testing.T) {
	e := reconnectEngine(t)
	if _, err := e.Peers.Allow("other", "ccdd", "", "", ""); err != nil {
		t.Fatal(err)
	}
	selected := lan.Endpoint{Addr: "192.168.1.10:47821", Route: lan.RouteLAN}
	other := lan.Endpoint{Addr: "192.168.1.30:47821", Route: lan.RouteLAN}
	target := NewDialTarget("selected", 0, nil)
	target.UpdateCandidates([]DialCandidate{
		{PeerID: "selected", Endpoint: selected},
		{PeerID: "other", Endpoint: other},
	})
	var attempts []string
	rounds := 0
	reconnect(context.Background(), target, e, func(_ context.Context, ep lan.Endpoint, id string, _ *Engine) error {
		attempts = append(attempts, id)
		return errors.New("offline")
	}, func(context.Context, time.Duration) bool {
		rounds++
		if rounds == 1 {
			if err := e.Peers.Forget("selected"); err != nil {
				t.Fatal(err)
			}
		}
		return rounds < 3
	})
	if !reflect.DeepEqual(attempts, []string{"selected", "other", "other", "other"}) {
		t.Fatalf("attempts: %v", attempts)
	}
}
