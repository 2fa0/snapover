package main

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/discovery"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/lan"
	"github.com/2fa0/snapover/internal/settings"
	clipsync "github.com/2fa0/snapover/internal/sync"
	"github.com/egoist/mygo/ui"
)

func TestAutomaticFallbackKeepsItsLiveController(t *testing.T) {
	a, b := interfaceTestApp(t), interfaceTestApp(t)
	left, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	right, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i, engine := range []*clipsync.Engine{a.engine, b.engine} {
		id := []*identity.Identity{left, right}[i]
		engine.LocalID, engine.Fingerprint = id.DeviceID, id.Fingerprint
		engine.TLS = identity.TLSConfig(id)
		engine.Board = &pasteTestBoard{}
		engine.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if _, err := a.engine.Peers.Allow(right.DeviceID, right.Fingerprint, "B", "macos", "192.168.1.30:47821"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.engine.Peers.Allow(left.DeviceID, left.Fingerprint, "A", "macos", "192.168.1.10:47821"); err != nil {
		t.Fatal(err)
	}
	rawA, rawB := net.Pipe()
	done := make(chan error, 2)
	go func() { done <- a.engine.Attach(tls.Client(rawA, a.engine.TLS), true) }()
	go func() { done <- b.engine.Attach(tls.Server(rawB, b.engine.TLS), false) }()
	t.Cleanup(func() {
		rawA.Close()
		rawB.Close()
		for range 2 {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("test connection did not stop")
			}
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for !a.engine.Connected() || !b.engine.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("test peers did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := a.engine.Peers.Allow(right.DeviceID, right.Fingerprint, "B", "macos", "192.168.1.30:47821"); err != nil {
		t.Fatal(err)
	}
	// Prevent an erroneous replacement controller from dialing a real network.
	root, stop := context.WithCancel(context.Background())
	stop()
	a.root = root
	dial, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.targetPeerID = "selected"
	a.dialTarget = clipsync.NewDialTarget("selected", a.dialSeq, nil)
	a.dialCancel = cancel
	target := a.dialTarget
	link, _ := a.engine.Link()
	a.apply(clipsync.Notice{Kind: clipsync.NoticeStatus, PeerID: right.DeviceID, Message: "已连接 B", SessionID: link.SessionID}, nil)
	defer a.stopDial()
	if dial.Err() != nil || a.dialTarget != target {
		t.Fatal("fallback success restarted the controller owning the live connection")
	}
	if a.targetPeerID != right.DeviceID {
		t.Fatal("the UI did not adopt the connected candidate")
	}
}

func dualAddressApp(t *testing.T) *app {
	t.Helper()
	a := interfaceTestApp(t)
	if _, err := a.engine.Peers.Allow("device", "aabb", "工作电脑", "windows", "192.168.1.20:47821"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.engine.Peers.AllowEndpoint("device", "aabb", "", "", lan.Endpoint{Addr: "100.65.1.20:47821", Route: lan.RouteTailscale}); err != nil {
		t.Fatal(err)
	}
	a.cfg.Values.AllowTailscale = true
	a = newApp(a.engine, a.hist, a.cfg, &identity.Identity{}, ":47821", nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.root = ctx
	return a
}

func TestDualAddressesRestoreSelectedIdentityAndColdSendTarget(t *testing.T) {
	a := dualAddressApp(t)
	if a.targetPeerID != "device" || a.coldTargetID != "device" || a.peer != "192.168.1.20:47821" {
		t.Fatalf("restored target: %s/%s/%s", a.targetPeerID, a.coldTargetID, a.peer)
	}
	if a.peerIDForAddress("100.65.1.20:47821") != "device" {
		t.Fatal("alternate address lost device identity")
	}
	a.connect()
	defer a.stopDial()
	paths := a.dialTarget.Endpoints()
	if len(paths) != 2 || paths[0].Route != lan.RouteLAN || paths[1].Route != lan.RouteTailscale {
		t.Fatalf("restore paths: %+v", paths)
	}
	a.targetPeerID = "other-device"
	a.peer = "100.65.1.99:47821"
	if a.coldTargetID != "device" {
		t.Fatal("cold file target changed with active selection")
	}
}

func TestEditingTailscaleAddressDoesNotOverwriteVerifiedAddress(t *testing.T) {
	a := dualAddressApp(t)
	before, _ := a.engine.Peers.Get("device")
	a.editTailscaleAddr = "100.65.1.99:49000"
	a.connectTailscaleAddress("device")
	defer a.stopDial()
	if a.dialTarget == nil || a.dialTarget.PeerID() != "device" {
		t.Fatal("address edit did not retain expected identity")
	}
	paths := a.dialTarget.Endpoints()
	if len(paths) != 2 || paths[1].Addr != a.editTailscaleAddr {
		t.Fatalf("edit not available as provisional candidate: %+v", paths)
	}
	if after, _ := a.engine.Peers.Get("device"); after != before {
		t.Fatal("unverified edit persisted")
	}
	seq, target := a.dialSeq, a.dialTarget
	a.applyDiscovery(discovery.Snapshot{Devices: []discovery.Device{{ID: "device", Addr: "192.168.2.20:47821"}}})
	if a.dialSeq != seq || a.dialTarget != target {
		t.Fatal("discovery canceled address validation")
	}
	if paths = a.dialTarget.Endpoints(); len(paths) != 3 || paths[0].Addr != "192.168.2.20:47821" || paths[2].Addr != a.editTailscaleAddr {
		t.Fatalf("discovery lost provisional tail address: %+v", paths)
	}
}

func TestEditingTailscaleOnlyAddressDoesNotInventLAN(t *testing.T) {
	a := interfaceTestApp(t)
	old := lan.Endpoint{Addr: "100.65.1.20:47821", Route: lan.RouteTailscale}
	if _, err := a.engine.Peers.AllowEndpoint("device", "aabb", "", "", old); err != nil {
		t.Fatal(err)
	}
	a.allowTailscale, a.targetPeerID = true, "device"
	a.dialProposed = lan.Endpoint{Addr: "100.65.1.99:49000", Route: lan.RouteTailscale}
	paths := a.connectionCandidates()
	if len(paths) != 1 || paths[0].Endpoint != a.dialProposed || paths[0].PeerID != "device" {
		t.Fatalf("editing invented a LAN path: %+v", paths)
	}
	if p, _ := a.engine.Peers.Get("device"); p.LANAddress() != "" || p.TailscaleAddr != old.Addr {
		t.Fatalf("unverified edit changed saved paths: %+v", p)
	}
}

func TestStartupExplicitPeerTakesPriorityOverLAN(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "restore"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			a := dualAddressApp(t)
			peer, wantRoute := "", lan.RouteLAN
			if explicit {
				peer, wantRoute = "100.65.1.20:47821", lan.RouteTailscale
			}
			a = newApp(a.engine, a.hist, a.cfg, &identity.Identity{}, ":47821", nil, peer)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			a.root = ctx
			attempted := make(chan clipsync.Notice, 1)
			a.engine.OnNotice = func(n clipsync.Notice) error {
				if n.Kind == clipsync.NoticeDial {
					// Observe the real controller's first choice, then cancel before
					// it can touch a real network or system clipboard.
					cancel()
					attempted <- n
				}
				return nil
			}
			a.connect()
			t.Cleanup(a.stopDial)
			select {
			case n := <-attempted:
				if n.Route != wantRoute || (explicit && n.Addr != peer) || n.PeerID != "device" {
					t.Fatalf("wrong first attempt: %+v", n)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("startup did not start reconnecting")
			}
		})
	}
}

func TestTailscaleSettingPersistsAndSaveFailureDoesNotEnableNetwork(t *testing.T) {
	a := interfaceTestApp(t)
	a.allowTailscale = true
	a.saveTailscaleSetting()
	restored, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || !restored.Values.AllowTailscale || !a.engine.TailscaleEnabled() {
		t.Fatalf("enable: %+v, %v", restored, err)
	}
	a.allowTailscale = false
	a.saveTailscaleSetting()
	if a.engine.TailscaleEnabled() {
		t.Fatal("disable did not reach engine")
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Path = filepath.Join(blocked, "settings.json")
	a.allowTailscale = true
	a.saveTailscaleSetting()
	if a.allowTailscale || a.cfg.Values.AllowTailscale || a.engine.TailscaleEnabled() || a.tailscaleError == "" {
		t.Fatal("failed save enabled networking")
	}
}

func TestRestrictedListenerAndDisabledTailscaleDoNotStartDial(t *testing.T) {
	a := interfaceTestApp(t)
	a.peer = "100.65.1.20"
	a.addressRoute = 1
	a.connectAddress()
	if a.dialCancel != nil || a.status != lan.ErrTailscaleDisabled.Error() {
		t.Fatal("disabled connection started")
	}
	a.listen = "127.0.0.1:47821"
	a.allowTailscale = true
	a.saveTailscaleSetting()
	if a.allowTailscale || a.engine.TailscaleEnabled() || a.tailscaleError == "" {
		t.Fatal("enabling widened restricted listener")
	}
}

func TestRemovingAddressKeepsDeviceAndDoesNotRestoreLegacyTail(t *testing.T) {
	a := dualAddressApp(t)
	a.connect()
	defer a.stopDial()
	a.removeTailscaleAddress("device")
	p, ok := a.engine.Peers.Get("device")
	if !ok || p.TailscaleAddr != "" || p.Addr != "192.168.1.20:47821" || a.targetPeerID != "device" {
		t.Fatalf("remove: %+v", p)
	}
	if paths := a.dialTarget.Endpoints(); len(paths) != 1 || paths[0].Route != lan.RouteLAN {
		t.Fatalf("removed address returned as candidate: %+v", paths)
	}
}

func TestOldDialNoticeCannotReplaceCurrentStatus(t *testing.T) {
	a := dualAddressApp(t)
	a.connect()
	defer a.stopDial()
	a.status = "current"
	for _, kind := range []string{clipsync.NoticeDial, clipsync.NoticeError, clipsync.NoticePairing} {
		a.apply(clipsync.Notice{Kind: kind, DialSeq: a.dialSeq + 1, Message: "old", Addr: "old"}, nil)
		if a.status != "current" {
			t.Fatalf("stale %s notice replaced status", kind)
		}
	}
}

func TestTailscaleControlsAndSavedAddressesAreAvailableInNativeUI(t *testing.T) {
	a := dualAddressApp(t)
	a.deviceDetails = "device"
	tt := ui.NewTester(a.view, 960, 900)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if !tt.HasText("局域网地址") || !tt.HasText("Tailscale 地址") || !tt.HasText("修改 Tailscale 地址") {
		t.Fatal("saved routes not visible")
	}
	clickInterface(t, tt, "修改 Tailscale 地址")
	if !tt.HasText("验证并连接") || a.editTailscaleAddr != "100.65.1.20:47821" {
		t.Fatal("address editor did not restore current address")
	}
	clickInterface(t, tt, "取消编辑")
	clickInterface(t, tt, "通过地址连接")
	if !tt.HasText("连接方式") || !tt.HasText("对端地址") {
		t.Fatal("manual route selection missing")
	}
	a.page = pageSettings
	tt.Frame()
	if !tt.HasText("允许通过 Tailscale 连接") {
		t.Fatal("Tailscale setting missing")
	}
}

func twoPeerApp(t *testing.T) *app {
	t.Helper()
	a := interfaceTestApp(t)
	if _, err := a.engine.Peers.Allow("home", "aaaa", "家里", "windows", "192.168.1.20:47821"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.engine.Peers.Allow("work", "bbbb", "公司", "macos", "192.168.1.30:47821"); err != nil {
		t.Fatal(err)
	}
	a = newApp(a.engine, a.hist, a.cfg, &identity.Identity{}, ":47821", nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.root = ctx
	return a
}

func candidateAddrs(cands []clipsync.DialCandidate) map[string]string {
	out := map[string]string{}
	for _, c := range cands {
		out[c.PeerID] = c.Endpoint.Addr
	}
	return out
}

func TestAutoConnectQueuesAllPairedDevices(t *testing.T) {
	a := twoPeerApp(t)
	a.connect()
	defer a.stopDial()
	got := candidateAddrs(a.dialTarget.Candidates())
	if got["home"] != "192.168.1.20:47821" || got["work"] != "192.168.1.30:47821" {
		t.Fatalf("auto candidates: %+v", a.dialTarget.Candidates())
	}
	if a.dialTarget.PeerID() == "" || a.dialPinned {
		t.Fatalf("auto dial pinned or missing preferred: %q %v", a.dialTarget.PeerID(), a.dialPinned)
	}
}

func TestConnectButtonPinsOnePairedDevice(t *testing.T) {
	a := twoPeerApp(t)
	a.connectDevice("home", "192.168.1.20:47821")
	defer a.stopDial()
	cands := a.dialTarget.Candidates()
	if !a.dialPinned || a.dialTarget.PeerID() != "home" || len(cands) != 1 || cands[0].Endpoint.Addr != "192.168.1.20:47821" {
		t.Fatalf("pinned: pinned=%v id=%s %+v", a.dialPinned, a.dialTarget.PeerID(), cands)
	}
}

func TestDiscoveryAddsOtherPairedDeviceWithoutRetargeting(t *testing.T) {
	a := twoPeerApp(t)
	a.connect()
	defer a.stopDial()
	preferred := a.targetPeerID
	a.applyDiscovery(discovery.Snapshot{Devices: []discovery.Device{
		{ID: "home", Addr: "192.168.8.20:47821"},
	}})
	if a.targetPeerID != preferred {
		t.Fatalf("discovery retargeted %s -> %s", preferred, a.targetPeerID)
	}
	found := false
	for _, c := range a.dialTarget.Candidates() {
		if c.PeerID == "home" && c.Endpoint.Addr == "192.168.8.20:47821" {
			found = true
		}
	}
	if !found {
		t.Fatalf("other paired discovery not queued: %+v", a.dialTarget.Candidates())
	}
}

func TestExplicitPeerDoesNotQueueOtherPairedDevice(t *testing.T) {
	a := twoPeerApp(t)
	a = newApp(a.engine, a.hist, a.cfg, &identity.Identity{}, ":47821", nil, "192.168.1.20:47821")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.root = ctx
	a.connect()
	defer a.stopDial()
	cands := a.dialTarget.Candidates()
	if !a.dialPinned || len(cands) != 1 || cands[0].PeerID != "home" || cands[0].Endpoint.Addr != "192.168.1.20:47821" {
		t.Fatalf("explicit queued others: pinned=%v %+v", a.dialPinned, cands)
	}
	a.applyDiscovery(discovery.Snapshot{Devices: []discovery.Device{
		{ID: "work", Addr: "192.168.8.30:47821"},
	}})
	for _, c := range a.dialTarget.Candidates() {
		if c.PeerID == "work" {
			t.Fatalf("pinned discovery queued work: %+v", a.dialTarget.Candidates())
		}
	}
}
