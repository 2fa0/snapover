package discovery

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/grandcat/zeroconf"
	"github.com/2fa0/snapover/internal/lan"
)

func TestOnlyPhysicalLANAddressesBecomeCandidates(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	_, vpn, _ := net.ParseCIDR("100.64.0.0/10")
	addrs := []lan.Addr{{IP: net.ParseIP("192.168.1.10"), Net: subnet}, {IP: net.ParseIP("100.64.0.1"), Net: vpn, Virtual: true}}
	id := "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name, id, ip string
		port         int
		want         bool
	}{
		{"LAN", id, "192.168.1.20", 47821, true},
		{"VPN", id, "100.64.0.2", 47821, false},
		{"public", id, "8.8.8.8", 47821, false},
		{"loopback", id, "127.0.0.1", 47821, false},
		{"multicast", id, "224.0.0.251", 47821, false},
		{"own identity", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "192.168.1.20", 47821, false},
		{"invalid identity", "invalid", "192.168.1.20", 47821, false},
		{"bad port", id, "192.168.1.20", 65536, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := &zeroconf.ServiceEntry{Port: tc.port, Text: []string{"id=" + tc.id, "name=Office\nPC", "platform=windows"}, AddrIPv4: []net.IP{net.ParseIP(tc.ip)}}
			dev, ok := parseEntry(entry, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", addrs)
			if ok != tc.want {
				t.Fatalf("candidate=%+v accepted=%v", dev, ok)
			}
			if ok && (dev.Name != "OfficePC" || dev.Addr != "192.168.1.20:47821") {
				t.Fatalf("bad metadata: %+v", dev)
			}
		})
	}
}

func TestDiscoveryExpiresAndDeduplicatesDevices(t *testing.T) {
	now := time.Now()
	seen := map[string]seenDevice{
		"live": {Device: Device{ID: "live", Addr: "new-address"}, at: now},
		"old":  {Device: Device{ID: "old"}, at: now.Add(-staleAfter - time.Second)},
	}
	got := snapshot(seen, "", now)
	if len(got.Devices) != 1 || got.Devices[0].ID != "live" || len(seen) != 1 {
		t.Fatalf("stale devices retained: %+v", got)
	}
}

func TestLANDiscovery(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_LAN") != "1" {
		t.Skip("set SNAPOVER_TEST_LAN=1 to test multicast on the current LAN")
	}
	if ifaces, _, _, _ := interfaces(":47821"); len(ifaces) == 0 {
		t.Skip("no physical multicast interface available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	left := Device{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "Snapover LAN test A", Platform: "macos", Addr: "47821"}
	right := Device{ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Name: "Snapover LAN test B", Platform: "windows", Addr: "47822"}
	found := make(chan string, 16)
	failures := make(chan string, 2)
	done := make(chan struct{}, 2)
	for _, pair := range [][2]Device{{left, right}, {right, left}} {
		local, remote := pair[0], pair[1]
		go func() {
			defer func() { done <- struct{}{} }()
			Run(ctx, local, ":"+local.Addr, func(s Snapshot) {
				if s.Message != "" {
					select {
					case failures <- s.Message:
					default:
					}
				}
				for _, dev := range s.Devices {
					if dev.ID == remote.ID && dev.Name == remote.Name {
						select {
						case found <- local.ID:
						default:
						}
					}
				}
			})
		}()
	}
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("discovery did not close sockets after cancellation")
			}
		}
	})
	seen := make(map[string]bool)
	for len(seen) != 2 {
		select {
		case id := <-found:
			seen[id] = true
		case message := <-failures:
			t.Fatal(message)
		case <-ctx.Done():
			t.Fatal("devices did not discover one another on the physical LAN")
		}
	}
}
