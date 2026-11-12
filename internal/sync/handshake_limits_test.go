package sync

import (
	"crypto/tls"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/wire"
)

func TestAuthenticationConnectionLimitAndRelease(t *testing.T) {
	left, _ := newIdentities(t)
	e := engineFor(t, left, &memBoard{})
	t.Cleanup(e.Stop)
	var clients []net.Conn
	done := make(chan error, maxAuthConnections+1)
	start := func() net.Conn {
		a, b := net.Pipe()
		clients = append(clients, b)
		go func() { done <- e.Attach(tls.Server(a, e.TLS), false) }()
		return b
	}
	t.Cleanup(func() {
		for _, c := range clients {
			_ = c.Close()
		}
		for range len(clients) {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("authentication did not release its connection")
			}
		}
	})
	count := func() int {
		e.authMu.Lock()
		defer e.authMu.Unlock()
		return len(e.authConns)
	}
	for i := 0; i < maxAuthConnections; i++ {
		start()
		await(t, func() bool { return count() == i+1 })
	}
	rejected := start()
	_ = rejected.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := rejected.Read(make([]byte, 1)); err == nil {
		t.Fatal("excess connection was not closed")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("excess connection waited instead of being rejected")
	}
	_ = clients[0].Close()
	await(t, func() bool { return count() == maxAuthConnections-1 })
	start()
	await(t, func() bool { return count() == maxAuthConnections })
}

func TestHandshakeStagesRejectOversizedFrames(t *testing.T) {
	for _, stage := range []string{"hello", "pair-state", "pair-choice", "pair-ready"} {
		t.Run(stage, func(t *testing.T) {
			left, right := newIdentities(t)
			e := engineFor(t, left, &memBoard{})
			e.SetPairing(true)
			client, server := handshakePair(t, left, right)
			t.Cleanup(func() { _ = client.NetConn().Close(); e.Stop() })
			done := make(chan error, 1)
			go func() { done <- e.Attach(server, false) }()
			if _, _, err := wire.ReadFrame(client); err != nil {
				t.Fatal(err)
			}
			if stage != "hello" {
				if err := wire.WriteMsg(client, wire.TypeHello, wire.Hello{
					DeviceID: right.DeviceID, Name: "peer", Proto: wire.Proto,
					Capabilities: []string{wire.PairCapability},
				}); err != nil {
					t.Fatal(err)
				}
				if _, _, err := wire.ReadFrame(client); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "pair-choice" || stage == "pair-ready" {
				if err := wire.WriteMsg(client, wire.TypePairState, wire.PairState{}); err != nil {
					t.Fatal(err)
				}
				await(t, func() bool { _, ok := e.PendingPair(); return ok })
			}
			if stage == "pair-ready" {
				pending, _ := e.PendingPair()
				e.ConfirmPair(pending.Token, true)
				if _, _, err := wire.ReadFrame(client); err != nil {
					t.Fatal(err)
				}
				if err := wire.WriteMsg(client, wire.TypePairChoice, wire.PairChoice{Accept: true}); err != nil {
					t.Fatal(err)
				}
				if _, _, err := wire.ReadFrame(client); err != nil {
					t.Fatal(err)
				}
			}
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], wire.MaxHandshakeFrame+1)
			if _, err := client.Write(header[:]); err != nil {
				t.Fatal(err)
			}
			// Authorization failures may send an error before closing the transport.
			go func() { _, _, _ = wire.ReadFrame(client) }()
			select {
			case err := <-done:
				if err == nil || (stage == "hello" || stage == "pair-state") && !strings.Contains(err.Error(), "帧长度") {
					t.Fatalf("oversized %s was not rejected: %v", stage, err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("oversized header waited for the body")
			}
			if e.Connected() || len(e.Peers.List()) != 0 {
				t.Fatal("oversized handshake created a trusted session")
			}
		})
	}
}
