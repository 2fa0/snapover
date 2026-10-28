package main

import (
	"context"
	"testing"

	"github.com/2fa0/snapover/internal/discovery"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/identity"
	clipsync "github.com/2fa0/snapover/internal/sync"
)

func TestRestorePairedDeviceAfterRestart(t *testing.T) {
	dir := t.TempDir()
	peers, err := clipsync.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peers.Allow("device", "fingerprint", "Windows", "windows", "192.168.1.20:47821"); err != nil {
		t.Fatal(err)
	}
	peers, err = clipsync.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := history.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	engine := &clipsync.Engine{Peers: peers}
	a := newApp(engine, hist, nil, &identity.Identity{}, ":47821", nil, "")
	if a.peer != "192.168.1.20:47821" || len(a.devices) != 1 {
		t.Fatalf("restored peer=%q devices=%v", a.peer, a.devices)
	}
	explicit := newApp(engine, hist, nil, &identity.Identity{}, ":47821", nil, "192.168.1.30:47821")
	if explicit.peer != "192.168.1.30:47821" {
		t.Fatal("explicit peer did not override the remembered device")
	}

	// A cancelled parent lets us check the reconnect target without dialing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.root = ctx
	a.connect()
	if a.dialAddr != a.peer || a.dialCancel == nil {
		t.Fatal("remembered device did not start a reconnect")
	}
	a.forget("device")
	if a.dialCancel != nil || a.dialAddr != "" {
		t.Fatal("removed device kept reconnecting")
	}
	peers, err = clipsync.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	engine = &clipsync.Engine{Peers: peers}
	a = newApp(engine, hist, nil, &identity.Identity{}, ":47821", nil, "")
	if a.peer != "" || len(a.devices) != 0 {
		t.Fatal("removed device returned after restart")
	}
}

func TestDiscoveryRetargetsOnlySelectedTrustedDevice(t *testing.T) {
	peers, err := clipsync.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = peers.Allow("selected", "aabb", "Selected", "macos", "192.168.1.20:47821")
	_, _ = peers.Allow("other", "ccdd", "Other", "windows", "192.168.1.30:47821")
	hist, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := &clipsync.Engine{Peers: peers}
	a := newApp(engine, hist, nil, &identity.Identity{}, ":47821", nil, "192.168.1.20:47821")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.root = ctx
	a.connect()
	defer a.stopDial()
	a.applyDiscovery(discovery.Snapshot{Devices: []discovery.Device{
		{ID: "untrusted", Addr: "192.168.1.40:47821"},
		{ID: "other", Addr: "192.168.1.50:47821"},
	}})
	if a.peer != "192.168.1.20:47821" || a.targetPeerID != "selected" {
		t.Fatal("unselected advertisement changed reconnect target")
	}
	a.applyDiscovery(discovery.Snapshot{Devices: []discovery.Device{{ID: "selected", Addr: "192.168.1.60:47821"}}})
	if a.dialAddr != "192.168.1.20:47821" || a.targetPeerID != "selected" {
		t.Fatal("discovery interrupted the active attempt")
	}
	if candidates := a.dialTarget.Endpoints(); len(candidates) != 2 || candidates[0].Addr != "192.168.1.60:47821" {
		t.Fatalf("selected device's new address was not queued: %+v", candidates)
	}
	for _, dev := range peers.List() {
		if dev.ID == "selected" && dev.Peer.Addr != "192.168.1.20:47821" {
			t.Fatal("unauthenticated discovery advertisement persisted an address")
		}
	}
	a.forget("selected")
	a.applyDiscovery(discovery.Snapshot{Devices: []discovery.Device{{ID: "selected", Addr: "192.168.1.70:47821"}}})
	if a.dialCancel != nil || a.targetPeerID != "" || a.peer != "" {
		t.Fatal("discovery restarted a revoked device")
	}
}

func TestDiscoveryDiscardsSnapshotAfterSelectionChange(t *testing.T) {
	a := &app{engine: &clipsync.Engine{}, discoveryMessage: "current"}
	a.discoveryScope.Store(discovery.Scope{})
	oldScope := discovery.Scope{All: true}
	a.applyDiscovery(discovery.Snapshot{
		Scope: &oldScope, Message: "old",
		Devices: []discovery.Device{{ID: "old", Addr: "192.168.1.20:47821"}},
	})
	if len(a.discovered) != 0 || a.discoveryMessage != "current" {
		t.Fatal("queued scan from the old selection restored discovery results")
	}
	currentScope := discovery.Scope{}
	a.applyDiscovery(discovery.Snapshot{Scope: &currentScope, Message: "stopped"})
	if a.discoveryMessage != "stopped" {
		t.Fatal("snapshot from the current selection was discarded")
	}
}
