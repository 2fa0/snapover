package lan

import (
	"net"
	"testing"
	"time"
)

func TestVirtualNames(t *testing.T) {
	virtual := []string{"utun4", "clash", "Tailscale", "vEthernet (WSL)", "gpd0", "Cisco AnyConnect", "awdl0"}
	physical := []string{"en0", "en1", "Ethernet", "Wi-Fi", "以太网"}
	for _, name := range virtual {
		if !Virtual(name) {
			t.Fatalf("%s should be virtual", name)
		}
	}
	for _, name := range physical {
		if Virtual(name) {
			t.Fatalf("%s should be physical", name)
		}
	}
}

func TestAllowAndBind(t *testing.T) {
	lanNet := mustNet(t, "192.168.1.0/24")
	vpnNet := mustNet(t, "10.8.0.0/24")
	ifaces := []Addr{
		{Name: "en0", IP: net.ParseIP("192.168.1.5").To4(), Net: lanNet, Virtual: false},
		{Name: "utun3", IP: net.ParseIP("10.8.0.2").To4(), Net: vpnNet, Virtual: true},
	}
	if !allowLocal(net.ParseIP("192.168.1.5"), ifaces) {
		t.Fatal("physical local address was rejected")
	}
	if allowLocal(net.ParseIP("10.8.0.2"), ifaces) {
		t.Fatal("vpn local address was accepted")
	}
	if !allowLocal(net.ParseIP("127.0.0.1"), ifaces) {
		t.Fatal("loopback was rejected")
	}
	if got := bindIP("192.168.1.20", ifaces).String(); got != "192.168.1.5" {
		t.Fatalf("same subnet bind = %s", got)
	}
	if got := bindIP("10.8.0.9", ifaces).String(); got != "192.168.1.5" {
		t.Fatalf("vpn target should still leave via physical nic, got %s", got)
	}
	if bindIP("127.0.0.1", ifaces) != nil {
		t.Fatal("loopback should not bind")
	}
}

func TestDialLoopbackDoesNotBind(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	conn, bind, err := DialTimeout(ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if bind != nil {
		t.Fatalf("loopback dial bound to %s", bind)
	}
}

func TestAllowLocalWithOverlappingSubnets(t *testing.T) {
	network := mustNet(t, "192.168.1.0/24")
	ifaces := []Addr{
		{Name: "utun3", IP: net.ParseIP("192.168.1.6"), Net: network, Virtual: true},
		{Name: "en0", IP: net.ParseIP("192.168.1.5"), Net: network},
	}
	for i := 0; i < 2; i++ {
		if !allowLocal(net.ParseIP("192.168.1.5"), ifaces) {
			t.Fatal("physical address was rejected")
		}
		if allowLocal(net.ParseIP("192.168.1.6"), ifaces) {
			t.Fatal("virtual address was accepted")
		}
		if !allowLocal(net.ParseIP("192.168.1.99"), ifaces) {
			t.Fatal("unclassified address was rejected because it shares a subnet")
		}
		ifaces[0], ifaces[1] = ifaces[1], ifaces[0]
	}
}

func mustNet(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
