package sync

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/2fa0/snapover/internal/lan"
)

func TestLegacyPeerPreservesLANWhenAddingTailscale(t *testing.T) {
	dir := t.TempDir()
	legacy := []byte(`{"peers":{"device":{"name":"old","fingerprint":"aabb","addr":"192.168.1.20:47821","trusted_at":"2026-10-27T00:00:00Z"}}}`)
	path := filepath.Join(dir, "peers.json")
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get("device")
	if data, _ := os.ReadFile(path); string(data) != string(legacy) {
		t.Fatal("loading rewrote old data")
	}
	tail := lan.Endpoint{Addr: "100.65.1.20:49000", Route: lan.RouteTailscale}
	if _, err := s.AllowEndpoint("device", "aabb", "", "", tail); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Get("device")
	if p.LANAddress() != before.Addr || p.TailscaleAddr != tail.Addr || p.Addr != tail.Addr || p.TrustedAt != before.TrustedAt {
		t.Fatalf("lost old address or trust: %+v", p)
	}
	if _, err := s.Allow("device", "aabb", "", "", "192.168.2.20:47821"); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Get("device")
	if p.TailscaleAddr != tail.Addr || p.LANAddr != "192.168.2.20:47821" {
		t.Fatalf("paths overwritten: %+v", p)
	}
	if len(p.Endpoints(false)) != 1 || len(p.Endpoints(true)) != 2 || !p.HasAddress(tail.Addr) {
		t.Fatal("candidate filtering lost a known address")
	}
	if _, err := s.AllowEndpoint("device", "ffff", "changed", "", lan.Endpoint{Addr: "100.65.1.99:47821", Route: lan.RouteTailscale}); err == nil {
		t.Fatal("changed fingerprint accepted")
	}
	if got, _ := s.Get("device"); !reflect.DeepEqual(got, p) {
		t.Fatal("failed identity check changed addresses")
	}
	if err := s.RemoveTailscaleAddress("device"); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Get("device")
	if p.TailscaleAddr != "" || p.Addr != p.LANAddr || p.Fingerprint != before.Fingerprint {
		t.Fatalf("deleting address revoked trust or lost LAN: %+v", p)
	}
}

func TestTailscaleOnlyPeerAndFailedAddressWrites(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ep := lan.Endpoint{Addr: "100.65.1.20:47821", Route: lan.RouteTailscale}
	if _, err := s.AllowEndpoint("device", "aabb", "", "", ep); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Get("device")
	if p.LANAddress() != "" || len(p.Endpoints(false)) != 0 || len(p.Endpoints(true)) != 1 {
		t.Fatalf("invented LAN address: %+v", p)
	}
	if err := os.Mkdir(s.path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	ep.Addr = "100.65.1.99:47821"
	if _, err := s.AllowEndpoint("device", "aabb", "changed", "", ep); err == nil {
		t.Fatal("expected write failure")
	}
	if err := s.RemoveTailscaleAddress("device"); err == nil {
		t.Fatal("expected remove failure")
	}
	if got, _ := s.Get("device"); !reflect.DeepEqual(got, p) {
		t.Fatal("failed save changed memory")
	}
	if err := os.Remove(s.path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTailscaleAddress("device"); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Get("device")
	if p.Addr != "" || p.LANAddress() != "" || p.TailscaleAddr != "" || p.Fingerprint != "aabb" {
		t.Fatalf("deleted address returned as legacy: %+v", p)
	}
}
