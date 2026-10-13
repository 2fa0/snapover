package sync

import (
	"errors"
	"os"
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

func TestFailedTrustWritesDoNotChangeMemory(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Allow("existing", "aabb", "original", "macos", "old"); err != nil {
		t.Fatal(err)
	}
	// An unwritable temporary-file destination forces the same failure on
	// every mutation without depending on platform permission semantics.
	if err := os.Mkdir(s.path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Allow("new", "ccdd", "new", "windows", "new"); err == nil {
		t.Fatal("expected save failure")
	}
	if _, err := s.Allow("existing", "aabb", "changed", "windows", "new"); err == nil {
		t.Fatal("expected update failure")
	}
	if err := s.Forget("existing"); err == nil {
		t.Fatal("expected forget failure")
	}
	if err := s.Reset(); err == nil {
		t.Fatal("expected reset failure")
	}
	devices := s.List()
	if len(devices) != 1 || devices[0].ID != "existing" || devices[0].Peer.Name != "original" || devices[0].Peer.Addr != "old" {
		t.Fatalf("failed save changed memory: %+v", devices)
	}
}
