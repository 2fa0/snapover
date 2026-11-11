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
	if f.Values.MaxFileMB != 20 {
		t.Fatalf("default file mb = %d", f.Values.MaxFileMB)
	}
	if f.Values.HistoryMaxMB != DefaultHistoryMB || f.Values.PickerShortcut != "" || f.Values.ManualSync || f.Values.CopyShortcut != "" || f.Values.KeepAwake || f.Values.AllowTailscale || f.Values.MenuBarIcon != DefaultMenuBarIcon {
		t.Fatalf("unexpected defaults: %+v", f.Values)
	}
	f.Values.Paused = true
	f.Values.MenuBarIcon = 3
	f.Values.TextOnly = true
	f.Values.MaxImageMB = 8
	f.Values.MaxTextMB = 10
	f.Values.MaxFileMB = 1024
	f.Values.HistoryMaxMB = 512
	f.Values.PickerShortcut = "Ctrl+Alt+V"
	f.Values.CopyShortcut = "Ctrl+Alt+C"
	f.Values.ManualSync = true
	f.Values.KeepAwake = true
	f.Values.AllowTailscale = true
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
	if !again.Values.Paused || !again.Values.TextOnly || again.Values.MaxImageMB != 8 || again.Values.ReceiveDir != dir || !again.Values.DisableSendTo || again.Values.MenuBarIcon != 3 {
		t.Fatalf("%+v", again.Values)
	}
	if again.Values.MaxTextMB != 10 {
		t.Fatalf("text mb = %d", again.Values.MaxTextMB)
	}
	if again.Values.MaxFileMB != 1024 {
		t.Fatalf("file mb = %d", again.Values.MaxFileMB)
	}
	if again.Values.HistoryMaxMB != 512 || again.Values.PickerShortcut != "Ctrl+Alt+V" || again.Values.CopyShortcut != "Ctrl+Alt+C" || !again.Values.ManualSync || !again.Values.KeepAwake || !again.Values.AllowTailscale {
		t.Fatalf("history or shortcut not restored: %+v", again.Values)
	}
	if again.Values.DiscoveryMode != "selected" || len(again.Values.DiscoveryNets) != 1 || again.Values.DiscoveryNets[0] != "en0/192.168.1.0/24" || len(again.Values.DiscoverySeen) != 2 {
		t.Fatalf("discovery selection not restored: %+v", again.Values)
	}
}

func TestIconDefaultsAndLegacySelection(t *testing.T) {
	for _, test := range []struct {
		json string
		want int
	}{
		{"", 2},
		{`{}`, 2},
		{`{"menu_bar_icon":0}`, 2},
		{`{"menu_bar_icon":1}`, 1},
		{`{"menu_bar_icon":2}`, 2},
		{`{"menu_bar_icon":3}`, 3},
		{`{"menu_bar_icon":-1}`, 2},
		{`{"menu_bar_icon":4}`, 2},
	} {
		dir := t.TempDir()
		if test.json != "" {
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(test.json), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cfg, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Values.MenuBarIcon != test.want {
			t.Fatalf("%s: icon=%d want=%d", test.json, cfg.Values.MenuBarIcon, test.want)
		}
		if err := cfg.Save(); err != nil {
			t.Fatal(err)
		}
		reopened, err := Load(dir)
		if err != nil || reopened.Values.MenuBarIcon != test.want {
			t.Fatalf("%s: selection did not survive save/reload: %+v %v", test.json, reopened, err)
		}
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

func TestFileLimitDefaultsAndBounds(t *testing.T) {
	for _, test := range []struct {
		json string
		want int64
	}{
		{`{"max_image_mb":8}`, 20},
		{`{"max_file_mb":0}`, 20},
		{`{"max_file_mb":-1}`, 20},
		{`{"max_file_mb":1}`, 1},
		{`{"max_file_mb":200}`, 200},
		{`{"max_file_mb":4096}`, 4096},
		{`{"max_file_mb":8796093022207}`, MaxFileMB},
		{`{"max_file_mb":9223372036854775807}`, MaxFileMB},
	} {
		t.Run(test.json, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(test.json), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Values.MaxFileMB != test.want {
				t.Fatalf("file limit = %d, want %d", cfg.Values.MaxFileMB, test.want)
			}
			if err := cfg.Save(); err != nil {
				t.Fatal(err)
			}
			again, err := Load(dir)
			if err != nil || again.Values.MaxFileMB != test.want {
				t.Fatalf("file limit not restored: %+v, %v", again, err)
			}
		})
	}
}

func TestPickerSizeDefaultsAndBounds(t *testing.T) {
	for _, test := range []struct {
		json          string
		width, height int
	}{
		{`{}`, DefaultPickerWidth, DefaultPickerHeight},
		{`{"picker_width":600,"picker_height":500}`, 600, 500},
		{`{"picker_width":0,"picker_height":0}`, DefaultPickerWidth, DefaultPickerHeight},
		{`{"picker_width":-20,"picker_height":-20}`, DefaultPickerWidth, DefaultPickerHeight},
		{`{"picker_width":100,"picker_height":100}`, DefaultPickerWidth, DefaultPickerHeight},
		{`{"picker_width":5000,"picker_height":5000}`, MaxPickerWidth, MaxPickerHeight},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(test.json), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if width, height := f.PickerSize(); width != test.width || height != test.height {
			t.Fatalf("%s: size = %d×%d, want %d×%d", test.json, width, height, test.width, test.height)
		}
	}
}

func TestLanguageDefaultsAndNormalization(t *testing.T) {
	for _, test := range []struct {
		json string
		want string
	}{
		{`{}`, LangAuto},
		{`{"language":""}`, LangAuto},
		{`{"language":"zh"}`, LangZH},
		{`{"language":"en"}`, LangEN},
		{`{"language":"fr"}`, LangAuto},
		{`{"language":"EN"}`, LangAuto},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(test.json), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if f.Values.Language != test.want {
			t.Fatalf("%s: language = %q, want %q", test.json, f.Values.Language, test.want)
		}
		if err := f.Save(); err != nil {
			t.Fatal(err)
		}
		reloaded, err := Load(dir)
		if err != nil || reloaded.Values.Language != test.want {
			t.Fatalf("%s: language not preserved across save: %+v, %v", test.json, reloaded, err)
		}
	}
}
