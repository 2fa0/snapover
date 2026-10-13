package sync

import (
	"context"
	"crypto/tls"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/wire"
)

func await(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for pairing state")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func startPair(t *testing.T, a, b *Engine, left, right *identity.Identity) (*tls.Conn, *tls.Conn, <-chan error) {
	t.Helper()
	client, server := handshakePair(t, left, right)
	done := make(chan error, 2)
	go func() { done <- a.Attach(server, false) }()
	go func() { done <- b.Attach(client, true) }()
	t.Cleanup(func() {
		a.Stop()
		b.Stop()
		_ = client.NetConn().Close()
		_ = server.NetConn().Close()
	})
	return client, server, done
}

func awaitRequests(t *testing.T, a, b *Engine) (PendingPair, PendingPair) {
	t.Helper()
	var left, right PendingPair
	await(t, func() bool {
		var lok, rok bool
		left, lok = a.PendingPair()
		right, rok = b.PendingPair()
		return lok && rok
	})
	if left.Code != right.Code || len(left.Code) != 7 {
		t.Fatalf("verification codes: %q, %q", left.Code, right.Code)
	}
	return left, right
}

func awaitPairEnd(t *testing.T, done <-chan error) {
	t.Helper()
	for range 2 {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("pairing did not end")
		}
	}
}

func TestPairingRequiresBothConfirmations(t *testing.T) {
	left, right := newIdentities(t)
	a, b := engineFor(t, left, &memBoard{}), engineFor(t, right, &memBoard{})
	a.SetPairing(true)
	b.SetPairing(true)
	_, _, _ = startPair(t, a, b, left, right)
	pa, pb := awaitRequests(t, a, b)
	checkBlocked := func() {
		t.Helper()
		if a.Connected() || b.Connected() || len(a.Peers.List()) != 0 || len(b.Peers.List()) != 0 {
			t.Fatal("unconfirmed peer was trusted or connected")
		}
		a.HandleLocal(1, mustText(t, "private before confirmation"))
		if len(b.Board.(*memBoard).Writes()) != 0 {
			t.Fatal("clipboard escaped before confirmation")
		}
		if _, err := a.SendFile(context.Background(), right.DeviceID, filepath.Join(t.TempDir(), "private"), nil); err == nil {
			t.Fatal("file transfer allowed before confirmation")
		}
	}
	checkBlocked()
	if a.ConfirmPair(pa.Token+1, true) {
		t.Fatal("stale pairing token accepted")
	}
	if !a.ConfirmPair(pa.Token, true) || a.ConfirmPair(pa.Token, true) {
		t.Fatal("confirmation was not single use")
	}
	checkBlocked()
	b.ConfirmPair(pb.Token, true)
	await(t, func() bool { return a.Connected() && b.Connected() })
	if len(a.Peers.List()) != 1 || len(b.Peers.List()) != 1 {
		t.Fatal("confirmed peer not persisted")
	}
	if time.Now().Before(a.PairingUntil()) || time.Now().Before(b.PairingUntil()) {
		t.Fatal("pairing window remained open after success")
	}
	item := mustText(t, "after confirmation")
	b.HandleLocal(2, item)
	a.mu.Lock()
	a.localActive = time.Time{}
	a.mu.Unlock()
	// Send once the local-priority rule is cleared.
	item = mustText(t, "confirmed clipboard")
	b.HandleLocal(3, item)
	waitWrite(t, a.Board.(*memBoard), item.Hash)
}

func TestPairingRejectCancelTimeoutAndRevokeLeaveNoTrust(t *testing.T) {
	for _, action := range []string{"reject", "cancel", "timeout", "revoke", "close"} {
		t.Run(action, func(t *testing.T) {
			left, right := newIdentities(t)
			a, b := engineFor(t, left, &memBoard{}), engineFor(t, right, &memBoard{})
			a.SetPairing(true)
			b.SetPairing(true)
			client, server, done := startPair(t, a, b, left, right)
			pa, _ := awaitRequests(t, a, b)
			switch action {
			case "reject":
				a.ConfirmPair(pa.Token, false)
			case "cancel":
				a.SetPairing(false)
			case "timeout":
				_ = server.SetDeadline(time.Now().Add(20 * time.Millisecond))
			case "revoke":
				if err := a.Revoke(right.DeviceID); err != nil {
					t.Fatal(err)
				}
			case "close":
				_ = client.NetConn().Close()
			}
			awaitPairEnd(t, done)
			if a.Connected() || b.Connected() || len(a.Peers.List()) != 0 || len(b.Peers.List()) != 0 {
				t.Fatal("failed pairing created trust or session")
			}
			if a.ConfirmPair(pa.Token, true) {
				t.Fatal("expired pairing prompt still accepted")
			}
		})
	}
}

func TestUnknownPeerCannotPairOutsideWindow(t *testing.T) {
	left, right := newIdentities(t)
	a, b := engineFor(t, left, &memBoard{}), engineFor(t, right, &memBoard{})
	_, _, done := startPair(t, a, b, left, right)
	awaitPairEnd(t, done)
	if _, ok := a.PendingPair(); ok || len(a.Peers.List()) != 0 {
		t.Fatal("unknown inbound peer opened a prompt outside pairing mode")
	}
}

func TestRevokeClosesSessionAndRequiresPairingAgain(t *testing.T) {
	a, b := newPair(t)
	peerID := b.LocalID
	if err := a.Revoke(peerID); err != nil {
		t.Fatal(err)
	}
	if a.Connected() || len(a.Peers.List()) != 0 {
		t.Fatal("revoked peer still connected or trusted")
	}
	await(t, func() bool { return !b.Connected() })
	// Reconstruct identities from the existing certificates to reconnect.
	left := &identity.Identity{TLS: a.TLS.Certificates[0]}
	right := &identity.Identity{TLS: b.TLS.Certificates[0]}
	_, _, done := startPair(t, a, b, left, right)
	awaitPairEnd(t, done)
	if a.Connected() || len(a.Peers.List()) != 0 {
		t.Fatal("peer regained trust after revocation")
	}
}

func TestOnlyOneSideTrustedRequiresBothToConfirm(t *testing.T) {
	left, right := newIdentities(t)
	a, b := engineFor(t, left, &memBoard{}), engineFor(t, right, &memBoard{})
	_, _ = a.Peers.Allow(right.DeviceID, right.Fingerprint, "old", "macos", "")
	a.SetPairing(true)
	b.SetPairing(true)
	_, _, _ = startPair(t, a, b, left, right)
	pa, pb := awaitRequests(t, a, b)
	b.ConfirmPair(pb.Token, true)
	if a.Connected() || b.Connected() {
		t.Fatal("one-sided trust bypassed mutual confirmation")
	}
	a.ConfirmPair(pa.Token, true)
	await(t, func() bool { return a.Connected() && b.Connected() })
}

func TestSelectedIdentityCannotBeSpoofedByDiscoveryAddress(t *testing.T) {
	left, right := newIdentities(t)
	a, b := engineFor(t, left, &memBoard{}), engineFor(t, right, &memBoard{})
	a.SetPairing(true)
	b.SetPairing(true)
	client, server := handshakePair(t, left, right)
	defer client.NetConn().Close()
	go func() { _ = b.Attach(client, true) }()
	err := a.AttachPeer(server, false, "another-device")
	if !errors.Is(err, ErrWrongPeer) || len(a.Peers.List()) != 0 {
		t.Fatalf("wrong identity accepted: %v", err)
	}
}

func TestLegacyPeersCanOnlyReconnectExistingTrust(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "trusted"}[trusted], func(t *testing.T) {
			left, right := newIdentities(t)
			a := engineFor(t, left, &memBoard{})
			if trusted {
				_, _ = a.Peers.Allow(right.DeviceID, right.Fingerprint, "old", "macos", "")
			}
			a.SetPairing(true)
			client, server := handshakePair(t, left, right)
			defer client.NetConn().Close()
			done := make(chan error, 1)
			go func() { done <- a.Attach(server, false) }()
			go func() {
				_ = wire.WriteMsg(client, wire.TypeHello, wire.Hello{DeviceID: right.DeviceID, Name: "old", Proto: wire.Proto})
				_, _, _ = wire.ReadFrame(client)
			}()
			if trusted {
				await(t, a.Connected)
				_ = client.NetConn().Close()
			} else {
				select {
				case err := <-done:
					if !errors.Is(err, ErrPairingStopped) || a.Connected() || len(a.Peers.List()) != 0 {
						t.Fatalf("unknown legacy peer accepted: %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("legacy peer was not rejected")
				}
			}
		})
	}
}

func TestSimultaneousPairingUsesSameConnection(t *testing.T) {
	left, right := newIdentities(t)
	a, b := engineFor(t, left, &memBoard{}), engineFor(t, right, &memBoard{})
	a.SetPairing(true)
	b.SetPairing(true)
	// Start the losing direction first, then the winning direction. The
	// displayed code and pending token must move to the same winning socket.
	if a.LocalID > b.LocalID {
		a, b = b, a
		left, right = right, left
	}
	_, _, _ = startPair(t, a, b, left, right) // larger id dials: loses
	oldA, _ := awaitRequests(t, a, b)
	_, _, _ = startPair(t, b, a, right, left) // smaller id dials: wins
	await(t, func() bool {
		pa, ok := a.PendingPair()
		return ok && pa.Token != oldA.Token
	})
	pa, pb := awaitRequests(t, a, b)
	if a.ConfirmPair(oldA.Token, true) {
		t.Fatal("old connection's token approved the replacement")
	}
	a.ConfirmPair(pa.Token, true)
	b.ConfirmPair(pb.Token, true)
	await(t, func() bool { return a.Connected() && b.Connected() })
}

func TestPairReadyDisconnectDoesNotPersistTrust(t *testing.T) {
	left, right := newIdentities(t)
	a := engineFor(t, left, &memBoard{})
	a.SetPairing(true)
	client, server := handshakePair(t, left, right)
	defer client.NetConn().Close()
	done := make(chan error, 1)
	go func() { done <- a.Attach(server, false) }()
	if err := exchangePair(client, wire.TypeHello, wire.Hello{DeviceID: right.DeviceID, Name: "remote", Proto: wire.Proto, Capabilities: []string{wire.PairCapability}}, &wire.Hello{}); err != nil {
		t.Fatal(err)
	}
	if err := exchangePair(client, wire.TypePairState, wire.PairState{}, &wire.PairState{}); err != nil {
		t.Fatal(err)
	}
	var p PendingPair
	await(t, func() bool { var ok bool; p, ok = a.PendingPair(); return ok })
	a.ConfirmPair(p.Token, true)
	if typ, _, err := wire.ReadFrame(client); err != nil || typ != wire.TypePairChoice {
		t.Fatalf("did not receive local confirmation: %d, %v", typ, err)
	}
	if err := wire.WriteMsg(client, wire.TypePairChoice, wire.PairChoice{Accept: true}); err != nil {
		t.Fatal(err)
	}
	_ = client.NetConn().Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("confirmation disconnect did not end pairing")
	}
	if len(a.Peers.List()) != 0 || a.Connected() {
		t.Fatal("incomplete final handshake persisted trust")
	}
}

func TestRevokedSessionCannotDeliverBufferedClipboard(t *testing.T) {
	a, b := newPair(t)
	a.mu.Lock()
	old := a.sess
	a.mu.Unlock()
	if err := a.Revoke(b.LocalID); err != nil {
		t.Fatal(err)
	}
	a.deliverFrom(old, mustText(t, "buffered before revocation"))
	if len(a.Board.(*memBoard).Writes()) != 0 {
		t.Fatal("revoked session wrote buffered clipboard")
	}
}

func TestSimultaneousPairingReconfirmsAfterConnectionSwitch(t *testing.T) {
	for _, confirmer := range []string{"smaller", "larger"} {
		t.Run(confirmer, func(t *testing.T) {
			left, right := newIdentities(t)
			a, b := engineFor(t, left, &memBoard{}), engineFor(t, right, &memBoard{})
			if a.LocalID > b.LocalID {
				a, b = b, a
				left, right = right, left
			}
			a.SetPairing(true)
			b.SetPairing(true)
			_, _, first := startPair(t, a, b, left, right)
			oldA, oldB := awaitRequests(t, a, b)
			if confirmer == "smaller" {
				a.ConfirmPair(oldA.Token, true)
			} else {
				b.ConfirmPair(oldB.Token, true)
			}
			_, _, _ = startPair(t, b, a, right, left)
			var pa, pb PendingPair
			await(t, func() bool {
				var lok, rok bool
				pa, lok = a.PendingPair()
				pb, rok = b.PendingPair()
				return lok && rok && pa.Token != oldA.Token && pb.Token != oldB.Token && pa.Code == pb.Code
			})
			awaitPairEnd(t, first)
			if pa.Confirmed || pb.Confirmed || a.Connected() || b.Connected() || len(a.Peers.List()) != 0 || len(b.Peers.List()) != 0 {
				t.Fatal("old confirmation was transferred to the replacement connection")
			}
			if a.ConfirmPair(oldA.Token, true) || b.ConfirmPair(oldB.Token, true) {
				t.Fatal("old token approved the replacement connection")
			}
			a.ConfirmPair(pa.Token, true)
			b.ConfirmPair(pb.Token, true)
			await(t, func() bool { return a.Connected() && b.Connected() })
		})
	}
}

func TestRemovedSessionCannotWriteQueuedClipboard(t *testing.T) {
	for _, action := range []string{"revoke", "revoke-all", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			left, right := newIdentities(t)
			a, b := engineFor(t, left, &memBoard{}), engineFor(t, right, &memBoard{})
			notices := make(chan Notice, 16)
			a.OnNotice = func(n Notice) { notices <- n }
			a.SetPairing(true)
			b.SetPairing(true)
			startPair(t, a, b, left, right)
			pa, pb := awaitRequests(t, a, b)
			a.ConfirmPair(pa.Token, true)
			b.ConfirmPair(pb.Token, true)
			await(t, func() bool { return a.Connected() && b.Connected() })
			a.mu.Lock()
			old := a.sess
			a.mu.Unlock()
			queued := make(chan func(), 1)
			finished := make(chan struct{})
			a.RunOnBoard = func(fn func()) {
				queued <- fn
				<-finished
			}
			item := mustText(t, "queued remote clipboard")
			done := make(chan struct{})
			go func() {
				a.deliverFrom(old, item)
				close(done)
			}()
			var write func()
			select {
			case write = <-queued:
			case <-time.After(3 * time.Second):
				t.Fatal("clipboard write was not queued")
			}
			switch action {
			case "revoke":
				if err := a.Revoke(b.LocalID); err != nil {
					t.Fatal(err)
				}
			case "revoke-all":
				if err := a.Revoke(""); err != nil {
					t.Fatal(err)
				}
			case "disconnect":
				a.Disconnect()
			}
			write()
			close(finished)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("queued clipboard delivery did not finish")
			}
			if len(a.Board.(*memBoard).Writes()) != 0 || a.Guard.IsEcho(item.Hash) {
				t.Fatal("removed session changed the clipboard or echo guard")
			}
			for len(notices) > 0 {
				if n := <-notices; n.Kind == NoticeRemote && n.Item.Kind != "" {
					t.Fatal("removed session emitted remote history")
				}
			}
		})
	}
}

func TestQueuedClipboardWritesForCurrentSession(t *testing.T) {
	a, _ := newPair(t)
	a.mu.Lock()
	current := a.sess
	a.mu.Unlock()
	called := false
	a.RunOnBoard = func(fn func()) { called = true; fn() }
	item := mustText(t, "current remote clipboard")
	a.deliverFrom(current, item)
	if !called || len(a.Board.(*memBoard).Writes()) != 1 || !a.Guard.IsEcho(item.Hash) {
		t.Fatal("current session did not write through the clipboard thread")
	}
}
