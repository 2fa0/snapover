package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/2fa0/snapover/internal/discovery"
	"github.com/2fa0/snapover/internal/lan"
	"github.com/2fa0/snapover/internal/settings"
)

func TestSegmentRowsGroupSubnetAndMarkVirtual(t *testing.T) {
	_, network, err := net.ParseCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatal(err)
	}
	_, vpn, err := net.ParseCIDR("100.64.0.0/10")
	if err != nil {
		t.Fatal(err)
	}
	rows := segmentRows([]lan.Addr{
		{Name: "en0", IP: net.ParseIP("192.168.1.23").To4(), Net: network},
		{Name: "en0", IP: net.ParseIP("192.168.1.24").To4(), Net: network},
		{Name: "utun3", IP: net.ParseIP("100.64.0.1").To4(), Net: vpn, Virtual: true},
	})
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].virtual || rows[0].key != "en0/192.168.1.0/24" || rows[0].label != "192.168.1.0/24 · en0 · 本机 192.168.1.23、192.168.1.24" {
		t.Fatalf("physical row = %+v", rows[0])
	}
	if !rows[1].virtual || rows[1].label != "100.64.0.0/10 · utun3 · 本机 100.64.0.1 · 不用于连接" {
		t.Fatalf("virtual row = %+v", rows[1])
	}
}

func TestDiscoverySelectionSaveFailureKeepsActiveScope(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &settings.File{Path: filepath.Join(blocked, "settings.json"), Values: settings.Values{MaxImageMB: 8}}
	a := &app{cfg: cfg, discoveryWake: make(chan struct{}, 1)}
	a.discoveryScope.Store(a.selection().Scope())
	a.setDiscovery(discovery.SelectNone([]string{"en0/192.168.1.0/24"}))
	if !a.selection().All() || !a.currentDiscoveryScope().All || len(a.discoveryWake) != 0 {
		t.Fatal("failed save changed the selection or active discovery")
	}
	if cfg.Values.MaxImageMB != 8 || cfg.Values.DiscoveryMode != "" || len(cfg.Values.DiscoveryNets) != 0 || len(cfg.Values.DiscoverySeen) != 0 {
		t.Fatalf("failed save left changed settings: %+v", cfg.Values)
	}
	if a.status != "保存设置失败" {
		t.Fatalf("status = %q", a.status)
	}
}

func TestDiscoverySelectionPersistsAndWakes(t *testing.T) {
	cfg, err := settings.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &app{cfg: cfg, discoveryWake: make(chan struct{}, 1)}
	key := "en0/192.168.1.0/24"
	a.setDiscovery(discovery.SelectNone([]string{key}))
	restored, err := settings.Load(filepath.Dir(cfg.Path))
	if err != nil {
		t.Fatal(err)
	}
	if !discovery.Remembered(restored.Values.DiscoveryMode, restored.Values.DiscoveryNets, restored.Values.DiscoverySeen).Stopped() || a.currentDiscoveryScope().Allows(key) || len(a.discoveryWake) != 1 {
		t.Fatal("stopped selection was not persisted and applied")
	}
	// Rapid edits must leave the latest scope and coalesce the wake signal.
	a.setDiscovery(discovery.SelectAll())
	if !a.currentDiscoveryScope().All || len(a.discoveryWake) != 1 {
		t.Fatal("latest scope was not applied")
	}
}
