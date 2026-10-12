package sync

import (
	"errors"
	"testing"
)

func TestPeerTrustSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Allow("device", "aabb", "Windows", "windows", "192.168.1.20:47821")
	if err != nil || !first {
		t.Fatalf("first=%v err=%v", first, err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	devices := s.List()
	if len(devices) != 1 || devices[0].Peer.Addr != "192.168.1.20:47821" {
		t.Fatalf("restored devices=%v", devices)
	}
	first, err = s.Allow("device", "aabb", "Windows", "windows", "192.168.1.20:47821")
	if err != nil || first {
		t.Fatalf("reconnect first=%v err=%v", first, err)
	}
	if _, err := s.Allow("device", "ccdd", "Windows", "windows", "192.168.1.20:47821"); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("changed fingerprint err=%v", err)
	}
}

func TestReconnectAddr(t *testing.T) {
	for _, tc := range []struct {
		name, remote, port, want string
		dialed                   bool
	}{
		{"outgoing", "192.168.1.20:49000", "47821", "192.168.1.20:49000", true},
		{"incoming", "192.168.1.20:61234", "47821", "192.168.1.20:47821", false},
		{"custom listener", "192.168.1.20:61234", "49000", "192.168.1.20:49000", false},
		{"older peer", "192.168.1.20:61234", "", "192.168.1.20:47821", false},
		{"IPv6", "[::1]:61234", "47821", "[::1]:47821", false},
		{"invalid port", "192.168.1.20:61234", "70000", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reconnectAddr(tc.remote, tc.dialed, tc.port); got != tc.want {
				t.Fatalf("address=%q want=%q", got, tc.want)
			}
		})
	}
}
