package discovery

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/lan"
	"github.com/grandcat/zeroconf"
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

func TestWakeCancelsBrowse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	wake := make(chan struct{}, 1)
	woke := watchWake(ctx, cancel, wake)
	wake <- struct{}{}
	<-ctx.Done()
	if ctx.Err() != context.Canceled || !woke() {
		t.Fatal("selection change did not cancel browse")
	}
}

func TestFinishedBrowseCannotConsumeNextWake(t *testing.T) {
	for range 100 {
		ctx, cancel := context.WithCancel(context.Background())
		wake := make(chan struct{}, 1)
		woke := watchWake(ctx, cancel, wake)
		cancel()
		if woke() {
			t.Fatal("ordinary cancellation was treated as a selection change")
		}
		wake <- struct{}{}
		runtime.Gosched()
		if len(wake) != 1 {
			t.Fatal("finished browse consumed the next selection change")
		}
		if !wait(context.Background(), time.Second, wake) || len(wake) != 0 {
			t.Fatal("idle wait did not consume the next selection change")
		}
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

func TestChooseInterfacesHonorsSelection(t *testing.T) {
	lanNet := mustCIDR(t, "192.168.1.0/24")
	other := mustCIDR(t, "10.0.0.0/24")
	vpn := mustCIDR(t, "100.64.0.0/10")
	addrs := []lan.Addr{
		{Name: "en0", IP: net.ParseIP("192.168.1.5").To4(), Net: lanNet},
		{Name: "en1", IP: net.ParseIP("10.0.0.8").To4(), Net: other},
		{Name: "utun3", IP: net.ParseIP("100.64.0.1").To4(), Net: vpn, Virtual: true},
	}
	lookup := func(name string) (*net.Interface, error) {
		index := map[string]int{"en0": 1, "en1": 2, "utun3": 3}[name]
		if index == 0 {
			return nil, net.ErrClosed
		}
		return &net.Interface{Index: index, Name: name, Flags: net.FlagUp | net.FlagMulticast}, nil
	}
	all := chooseInterfaces(":47821", Scope{All: true}, addrs, lookup)
	if len(all.ifaces) != 2 || len(all.ips) != 2 || all.empty != 0 {
		t.Fatalf("all physical segments = %+v", all)
	}
	only := chooseInterfaces(":47821", Scope{Keys: map[string]bool{lan.SegmentKey(addrs[0]): true}}, addrs, lookup)
	if len(only.ifaces) != 1 || len(only.ips) != 1 || only.ips[0] != "192.168.1.5" {
		t.Fatalf("selected segment = %+v", only)
	}
	none := chooseInterfaces(":47821", Scope{}, addrs, lookup)
	if none.empty != emptyUnselected || len(none.ifaces) != 0 || none.signature != "unselected" {
		t.Fatalf("empty selection = %+v", none)
	}
	missing := chooseInterfaces(":47821", Scope{All: true}, nil, lookup)
	if missing.empty != emptyNone {
		t.Fatalf("no interface = %+v", missing)
	}
	bound := chooseInterfaces("10.0.0.8:47821", Scope{All: true}, addrs, lookup)
	if len(bound.ips) != 1 || bound.ips[0] != "10.0.0.8" {
		t.Fatalf("bound listen did not stay on its address: %+v", bound)
	}
}

func mustCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	return network
}

func TestLANDiscovery(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_LAN") != "1" {
		t.Skip("set SNAPOVER_TEST_LAN=1 to test multicast on the current LAN")
	}
	if chosen := interfaces(":47821", Scope{All: true}); len(chosen.ifaces) == 0 {
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
			Run(ctx, local, ":"+local.Addr, nil, nil, func(s Snapshot) {
				if s.Message != "" {
					message := s.Message
					if s.Err != nil {
						message = fmt.Sprintf("%s: %v", message, s.Err)
					}
					select {
					case failures <- message:
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
	var lastFailure string
	for len(seen) != 2 {
		select {
		case id := <-found:
			seen[id] = true
		case message := <-failures:
			// macOS can reject the first operation while its permission
			// prompt is pending. Keep the process alive and let Run retry.
			lastFailure = message
		case <-ctx.Done():
			if lastFailure != "" {
				if runtime.GOOS == "darwin" {
					t.Log("请在系统设置 → 隐私与安全性 → 本地网络中允许运行测试的宿主应用（例如 Cindy），或从 Terminal 中运行此测试。")
				}
				t.Fatal(lastFailure)
			}
			t.Fatal("devices did not discover one another on the physical LAN")
		}
	}
}
