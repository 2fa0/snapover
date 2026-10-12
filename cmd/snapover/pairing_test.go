package main

import (
	"context"
	"testing"

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
	engine.Peers = peers
	a = newApp(engine, hist, nil, &identity.Identity{}, ":47821", nil, "")
	if a.peer != "" || len(a.devices) != 0 {
		t.Fatal("removed device returned after restart")
	}
}
