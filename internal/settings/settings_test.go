package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	f, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Values.MaxImageMB != DefaultMaxImageMB {
		t.Fatalf("default mb = %d", f.Values.MaxImageMB)
	}
	if f.Values.MaxTextMB != DefaultMaxTextMB {
		t.Fatalf("default text mb = %d", f.Values.MaxTextMB)
	}
	if f.Values.HistoryMaxMB != DefaultHistoryMB || f.Values.PickerShortcut != "" || f.Values.ManualSync || f.Values.CopyShortcut != "" || f.Values.KeepAwake {
		t.Fatalf("unexpected defaults: %+v", f.Values)
	}
	f.Values.Paused = true
	f.Values.TextOnly = true
	f.Values.MaxImageMB = 8
	f.Values.MaxTextMB = 10
	f.Values.HistoryMaxMB = 512
	f.Values.PickerShortcut = "Ctrl+Alt+V"
	f.Values.CopyShortcut = "Ctrl+Alt+C"
	f.Values.ManualSync = true
	f.Values.KeepAwake = true
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
	if again.Values.MaxTextMB != 10 {
		t.Fatalf("text mb = %d", again.Values.MaxTextMB)
	}
	if again.Values.HistoryMaxMB != 512 || again.Values.PickerShortcut != "Ctrl+Alt+V" || again.Values.CopyShortcut != "Ctrl+Alt+C" || !again.Values.ManualSync || !again.Values.KeepAwake {
		t.Fatalf("history or shortcut not restored: %+v", again.Values)
	}
	if again.Values.DiscoveryMode != "selected" || len(again.Values.DiscoveryNets) != 1 || again.Values.DiscoveryNets[0] != "en0/192.168.1.0/24" || len(again.Values.DiscoverySeen) != 2 {
		t.Fatalf("discovery selection not restored: %+v", again.Values)
	}
}

func TestTextLimitDefaultsAndBounds(t *testing.T) {
	for _, test := range []struct {
		json string
		want int
	}{
		{`{"max_image_mb":8}`, DefaultMaxTextMB},
		{`{"max_text_mb":0}`, DefaultMaxTextMB},
		{`{"max_text_mb":-1}`, DefaultMaxTextMB},
		{`{"max_text_mb":100}`, MaxTextMB},
		{`{"max_text_mb":20}`, MaxTextMB},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(test.json), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(dir)
		if err != nil || cfg.Values.MaxTextMB != test.want {
			t.Fatalf("%s: cfg=%+v err=%v", test.json, cfg, err)
		}
	}
}

func TestClampImageLimit(t *testing.T) {
	if clampMB(0) != DefaultMaxImageMB || clampMB(100) != DefaultMaxImageMB || clampMB(3) != 3 {
		t.Fatal("clamp")
	}
}
