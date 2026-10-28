package lan

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestTailscaleAddressAndInterfaceBoundaries(t *testing.T) {
	for _, test := range []struct {
		ip   string
		want bool
	}{
		{"100.63.255.255", false}, {"100.64.0.0", true}, {"100.127.255.255", true},
		{"100.128.0.0", false}, {"192.168.1.2", false}, {"fd7a:115c:a1e0::1", false}, {"", false},
	} {
		if got := TailscaleIP(net.ParseIP(test.ip)); got != test.want {
			t.Errorf("%s: %v", test.ip, got)
		}
	}
	for _, test := range []struct {
		name, ip      string
		virtual, want bool
	}{
		{"utun9", "100.64.1.2", true, true}, {"Tailscale", "100.65.1.2", true, true},
		{"en0", "100.64.1.2", false, false}, {"utun9", "10.8.0.1", true, false},
		{"Clash", "100.64.1.2", true, false}, {"vEthernet", "100.64.1.2", true, false},
	} {
		if got := TailscaleInterface(Addr{Name: test.name, IP: net.ParseIP(test.ip), Virtual: test.virtual}); got != test.want {
			t.Errorf("%+v: %v", test, got)
		}
	}
}

func TestTailscaleRouteRequiresExplicitOptInAndTunnel(t *testing.T) {
	physical := Addr{Name: "en0", IP: net.ParseIP("192.168.1.5"), Net: mustNet(t, "192.168.1.0/24")}
	tunnel := Addr{Name: "utun9", IP: net.ParseIP("100.64.1.5"), Virtual: true}
	ep := Endpoint{Addr: "100.65.1.6:47821", Route: RouteTailscale}
	ifaces := []Addr{physical, tunnel}
	if _, err := routeBind(ep, false, ifaces); !errors.Is(err, ErrTailscaleDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	if _, err := routeBind(ep, true, []Addr{physical}); !errors.Is(err, ErrNoTailscale) {
		t.Fatalf("missing tunnel fell back to LAN: %v", err)
	}
	if bind, err := routeBind(ep, true, ifaces); err != nil || !bind.Equal(tunnel.IP) {
		t.Fatalf("tunnel binding: %v, %v", bind, err)
	}
	ep.Route = RouteLAN
	if bind, err := routeBind(ep, true, ifaces); err != nil || !bind.Equal(physical.IP) {
		t.Fatalf("LAN routing changed: %v, %v", bind, err)
	}
	ifaces = append(ifaces, Addr{Name: "utun8", IP: net.ParseIP("100.66.1.5"), Virtual: true})
	ep.Route = RouteTailscale
	if _, err := routeBind(ep, true, ifaces); err == nil {
		t.Fatal("ambiguous tunnels were selected arbitrarily")
	}
}

func TestIncomingRouteDoesNotEnableOtherVPNsOrPhysicalCGNAT(t *testing.T) {
	ifaces := []Addr{
		{Name: "en0", IP: net.ParseIP("100.64.1.9")},
		{Name: "utun9", IP: net.ParseIP("100.64.1.5"), Virtual: true},
		{Name: "utun8", IP: net.ParseIP("10.8.0.1"), Virtual: true},
	}
	for _, test := range []struct {
		local, remote string
		enabled       bool
		want          Route
		allowed       bool
	}{
		{"100.64.1.5", "100.65.1.6", false, "", false},
		{"100.64.1.5", "100.65.1.6", true, RouteTailscale, true},
		{"100.64.1.5", "192.168.1.6", true, "", false},
		{"10.8.0.1", "10.8.0.2", true, "", false},
		{"100.64.1.9", "100.65.1.6", true, RouteLAN, true},
		{"127.0.0.1", "127.0.0.1", false, RouteLAN, true},
	} {
		route, err := incomingRoute(net.ParseIP(test.local), net.ParseIP(test.remote), test.enabled, ifaces)
		if (err == nil) != test.allowed || route != test.want {
			t.Errorf("%+v: %s, %v", test, route, err)
		}
	}
}

func TestEndpointRejectsInvalidTailscaleTargets(t *testing.T) {
	for _, addr := range []string{"host:47821", "192.168.1.5:47821", "100.128.0.1:47821", "[fd7a:115c:a1e0::1]:47821", "100.64.1.5:0", "100.64.1.5:65536", "100.64.1.5"} {
		if err := (Endpoint{Addr: addr, Route: RouteTailscale}).Validate(); err == nil {
			t.Errorf("accepted %q", addr)
		}
	}
	if err := (Endpoint{Addr: "100.64.1.5:49000", Route: RouteTailscale}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDialContextCancellationAndRestrictedListener(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if conn, _, err := DialContext(ctx, Endpoint{Addr: "127.0.0.1:47821", Route: RouteLAN}, false, time.Second); conn != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled dial: %v, %v", conn, err)
	}
	for _, addr := range []string{":47821", "0.0.0.0:49000", "[::]:47821"} {
		if err := CheckTailscaleListener(addr); err != nil {
			t.Errorf("wildcard %s: %v", addr, err)
		}
	}
	for _, addr := range []string{"127.0.0.1:47821", "192.0.2.1:47821"} {
		if err := CheckTailscaleListener(addr); err == nil {
			t.Errorf("restricted listener accepted: %s", addr)
		}
	}
}
