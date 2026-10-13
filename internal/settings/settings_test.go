package settings

import "testing"

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	f, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Values.MaxImageMB != DefaultMaxImageMB {
		t.Fatalf("default mb = %d", f.Values.MaxImageMB)
	}
	f.Values.Paused = true
	f.Values.TextOnly = true
	f.Values.MaxImageMB = 8
	f.Values.ReceiveDir = dir
	f.Values.DisableSendTo = true
	f.Values.DiscoveryMode = "selected"
	f.Values.DiscoveryNets = []string{"en0/192.168.1.0/24"}
	f.Values.DiscoverySeen = []string{"en0/192.168.1.0/24", "en1/10.0.0.0/24"}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Values.Paused || !again.Values.TextOnly || again.Values.MaxImageMB != 8 || again.Values.ReceiveDir != dir || !again.Values.DisableSendTo {
		t.Fatalf("%+v", again.Values)
	}
	if again.Values.DiscoveryMode != "selected" || len(again.Values.DiscoveryNets) != 1 || again.Values.DiscoveryNets[0] != "en0/192.168.1.0/24" || len(again.Values.DiscoverySeen) != 2 {
		t.Fatalf("discovery selection not restored: %+v", again.Values)
	}
}

func TestClampImageLimit(t *testing.T) {
	if clampMB(0) != DefaultMaxImageMB || clampMB(100) != DefaultMaxImageMB || clampMB(3) != 3 {
		t.Fatal("clamp")
	}
}
