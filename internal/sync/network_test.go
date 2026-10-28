package sync

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/lan"
)

type routeTestConn struct {
	net.Conn
	local, remote *net.TCPAddr
}

func (c routeTestConn) LocalAddr() net.Addr  { return c.local }
func (c routeTestConn) RemoteAddr() net.Addr { return c.remote }

// Only the socket addresses are simulated; TLS, pairing and sessions are real.
func startTailscalePair(t *testing.T) (*Engine, *Engine, <-chan error) {
	t.Helper()
	left, right := newIdentities(t)
	a, b := engineFor(t, left, &memBoard{}), engineFor(t, right, &memBoard{})
	a.SetAllowTailscale(true)
	b.SetAllowTailscale(true)
	a.SetPairing(true)
	b.SetPairing(true)
	rawA, rawB := net.Pipe()
	addrA := &net.TCPAddr{IP: net.ParseIP("100.64.1.1"), Port: 47821}
	addrB := &net.TCPAddr{IP: net.ParseIP("100.65.1.2"), Port: 51763}
	server := tls.Server(routeTestConn{Conn: rawA, local: addrA, remote: addrB}, a.TLS)
	client := tls.Client(routeTestConn{Conn: rawB, local: addrB, remote: addrA}, b.TLS)
	done := make(chan error, 2)
	go func() { done <- a.attachPeer(context.Background(), server, false, "", lan.RouteTailscale) }()
	go func() { done <- b.attachPeer(context.Background(), client, true, "", lan.RouteTailscale) }()
	t.Cleanup(func() { a.Stop(); b.Stop(); _ = rawA.Close(); _ = rawB.Close() })
	return a, b, done
}

func TestTailscaleRequiresBothConfirmationsAndPreservesAddressesWhenDisabled(t *testing.T) {
	a, b, done := startTailscalePair(t)
	pa, pb := awaitRequests(t, a, b)
	a.ConfirmPair(pa.Token, true)
	if a.Connected() || b.Connected() || len(a.Peers.List()) != 0 || len(b.Peers.List()) != 0 {
		t.Fatal("Tailscale bypassed confirmation")
	}
	b.ConfirmPair(pb.Token, true)
	await(t, func() bool { return a.Connected() && b.Connected() })
	p, _ := a.Peers.Get(b.LocalID)
	if p.TailscaleAddr != "100.65.1.2:47821" || p.LANAddress() != "" {
		t.Fatalf("wrong reconnect address: %+v", p)
	}
	if link, _ := a.Link(); link.Route != lan.RouteTailscale {
		t.Fatal("missing live route")
	}
	item := mustText(t, "Tailscale test text")
	a.HandleLocal(1, item)
	waitWrite(t, b.Board.(*memBoard), item.Hash)
	a.SetAllowTailscale(false)
	if a.Connected() {
		t.Fatal("disabled tunnel remains connected")
	}
	awaitPairEnd(t, done)
	if after, _ := a.Peers.Get(b.LocalID); after != p {
		t.Fatal("disable removed a saved address")
	}
}

func TestDisablePendingTailscalePairCannotPersistTrustAfterReenable(t *testing.T) {
	a, b, done := startTailscalePair(t)
	pa, pb := awaitRequests(t, a, b)
	a.ConfirmPair(pa.Token, true)
	a.SetAllowTailscale(false)
	a.SetAllowTailscale(true)
	b.ConfirmPair(pb.Token, true)
	awaitPairEnd(t, done)
	if a.Connected() || len(a.Peers.List()) != 0 || len(b.Peers.List()) != 0 {
		t.Fatal("old handshake restored trust after disabling")
	}
}

func TestDisableTailscaleKeepsLANSession(t *testing.T) {
	a, b := newPair(t)
	a.SetAllowTailscale(true)
	ctx, finish, err := a.tailscaleContext(context.Background(), b.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	a.SetAllowTailscale(false)
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("pending tunnel dial was not canceled")
	}
	if !a.Connected() || !b.Connected() {
		t.Fatal("disabling Tailscale dropped LAN session")
	}
	a.SetAllowTailscale(true)
	if ctx.Err() == nil {
		t.Fatal("re-enabling revived the old dial")
	}
}

func TestRemovingTailscaleAddressClosesSessionWithoutRevokingDevice(t *testing.T) {
	a, b, done := startTailscalePair(t)
	pa, pb := awaitRequests(t, a, b)
	a.ConfirmPair(pa.Token, true)
	b.ConfirmPair(pb.Token, true)
	await(t, func() bool { return a.Connected() && b.Connected() })
	if err := a.RemoveTailscaleAddress(b.LocalID); err != nil {
		t.Fatal(err)
	}
	awaitPairEnd(t, done)
	p, ok := a.Peers.Get(b.LocalID)
	if !ok || p.TailscaleAddr != "" || p.Addr != "" || a.Connected() {
		t.Fatalf("address deletion lost trust or kept endpoint: %+v", p)
	}
}

// Block one application write after TLS has completed, so address deletion can
// overlap either Hello (identity still unknown) or a trusted PairState exchange.
type gatedNetworkConn struct {
	net.Conn
	active  bool
	writes  int
	blockAt int
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *gatedNetworkConn) Write(p []byte) (int, error) {
	if c.active {
		c.writes++
		if c.writes == c.blockAt {
			close(c.entered)
			<-c.release
		}
	}
	return c.Conn.Write(p)
}

func (c *gatedNetworkConn) unblock() {
	c.once.Do(func() { close(c.release) })
}

func startBlockedTailscaleReconnect(t *testing.T, known bool) (*Engine, *Engine, *gatedNetworkConn, <-chan error) {
	t.Helper()
	left, right := newIdentities(t)
	a, b := engineFor(t, left, &memBoard{}), engineFor(t, right, &memBoard{})
	a.SetAllowTailscale(true)
	b.SetAllowTailscale(true)
	if _, err := a.Peers.AllowEndpoint(b.LocalID, right.Fingerprint, "", "", lan.Endpoint{Addr: "100.65.1.2:47821", Route: lan.RouteTailscale}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Peers.AllowEndpoint(a.LocalID, left.Fingerprint, "", "", lan.Endpoint{Addr: "100.64.1.1:47821", Route: lan.RouteTailscale}); err != nil {
		t.Fatal(err)
	}
	rawA, rawB := net.Pipe()
	blockAt := 1
	if known {
		blockAt = 2
	}
	gate := &gatedNetworkConn{Conn: rawB, blockAt: blockAt, entered: make(chan struct{}), release: make(chan struct{})}
	addrA := &net.TCPAddr{IP: net.ParseIP("100.64.1.1"), Port: 47821}
	addrB := &net.TCPAddr{IP: net.ParseIP("100.65.1.2"), Port: 51763}
	server := tls.Server(routeTestConn{Conn: rawA, local: addrA, remote: addrB}, a.TLS)
	client := tls.Client(routeTestConn{Conn: gate, local: addrB, remote: addrA}, b.TLS)
	t.Cleanup(func() { gate.unblock(); a.Stop(); b.Stop(); _ = rawA.Close(); _ = rawB.Close() })
	hs := make(chan error, 1)
	go func() { hs <- server.Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-hs; err != nil {
		t.Fatal(err)
	}
	gate.active = true
	expectedID := ""
	if known {
		expectedID = b.LocalID
	}
	done := make(chan error, 2)
	go func() { done <- a.attachPeer(context.Background(), server, false, expectedID, lan.RouteTailscale) }()
	go func() { done <- b.attachPeer(context.Background(), client, true, a.LocalID, lan.RouteTailscale) }()
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handshake did not reach gate")
	}
	await(t, func() bool {
		a.authMu.Lock()
		defer a.authMu.Unlock()
		_, registered := a.authConns[server]
		return registered
	})
	return a, b, gate, done
}

func TestAddressDeletionAffectsOnlyTargetHandshake(t *testing.T) {
	for _, tc := range []struct {
		name     string
		known    bool
		samePeer bool
		failSave bool
	}{
		{name: "other_known", known: true},
		{name: "other_unknown"},
		{name: "same_known", known: true, samePeer: true},
		{name: "same_unknown", samePeer: true},
		{name: "save_failure", samePeer: true, failSave: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b, gate, done := startBlockedTailscaleReconnect(t, tc.known)
			id := b.LocalID
			if !tc.samePeer {
				id = "other-peer"
				if _, err := a.Peers.AllowEndpoint(id, "aabb", "", "", lan.Endpoint{Addr: "100.65.1.99:47821", Route: lan.RouteTailscale}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.failSave {
				if err := os.Mkdir(a.Peers.path+".tmp", 0700); err != nil {
					t.Fatal(err)
				}
			}
			err := a.RemoveTailscaleAddress(id)
			if (err != nil) != tc.failSave {
				t.Fatalf("deleting address: %v", err)
			}
			if tc.failSave {
				if err := os.Remove(a.Peers.path + ".tmp"); err != nil {
					t.Fatal(err)
				}
			}
			gate.unblock()
			if !tc.samePeer || tc.failSave {
				await(t, func() bool { return a.Connected() && b.Connected() })
				if !a.TailscaleEnabled() {
					t.Fatal("address deletion disabled Tailscale")
				}
				a.Disconnect()
				b.Disconnect()
			} else {
				p, ok := a.Peers.Get(id)
				if !ok || p.TailscaleAddr != "" {
					t.Fatal("address deletion lost trust or retained the endpoint")
				}
			}
			awaitPairEnd(t, done)
			if tc.samePeer && !tc.failSave {
				if p, _ := a.Peers.Get(id); p.TailscaleAddr != "" || a.Connected() {
					t.Fatal("older handshake restored a deleted endpoint")
				}
			}
		})
	}
}

func TestRevokingPeerClearsTailscaleRemovalState(t *testing.T) {
	e := testEngine(t, &memBoard{})
	for _, id := range []string{"a", "b"} {
		if _, err := e.Peers.AllowEndpoint(id, "aabb", "", "", lan.Endpoint{Addr: "100.65.1.20:47821", Route: lan.RouteTailscale}); err != nil {
			t.Fatal(err)
		}
		if err := e.RemoveTailscaleAddress(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Revoke("a"); err != nil {
		t.Fatal(err)
	}
	if _, exists := e.tailscaleRemovals["a"]; exists || e.tailscaleRemovals["b"] == 0 {
		t.Fatal("revoking one peer did not preserve the other's removal state")
	}
	if err := e.Revoke(""); err != nil {
		t.Fatal(err)
	}
	if len(e.tailscaleRemovals) != 0 {
		t.Fatal("revoked devices retained removal state")
	}
}
