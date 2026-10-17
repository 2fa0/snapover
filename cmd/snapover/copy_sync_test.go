package main

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/settings"
)

func TestCopyShortcutDefaultsAndConflicts(t *testing.T) {
	key, err := configuredCopyShortcut("")
	want := "Ctrl+Alt+C"
	if runtime.GOOS == "darwin" {
		want = "Super+Alt+C"
	}
	if err != nil || key != want {
		t.Fatalf("default %q %v", key, err)
	}
	for _, reserved := range []string{"Ctrl+C", "Cmd+C"} {
		if _, err := configuredCopyShortcut(reserved); err == nil {
			t.Fatal("intercepted ordinary copy")
		}
		if _, err := configuredPickerShortcuts(reserved); err == nil {
			t.Fatal("history intercepts injected copy")
		}
	}
	a := interfaceTestApp(t)
	a.registeredPickerShortcuts = []string{"Alt+V"}
	a.saveCopyShortcut("Alt+V")
	if a.copyShortcutError == "" || a.cfg.Values.CopyShortcut != "" {
		t.Fatal("copy stole history shortcut")
	}
	a.registeredCopyShortcuts = []string{want}
	a.savePickerShortcut(want)
	if a.shortcutError == "" || a.cfg.Values.PickerShortcut != "" {
		t.Fatal("history stole copy shortcut")
	}
}

func TestSyncModeSettingPersistsAndRollsBack(t *testing.T) {
	a := interfaceTestApp(t)
	tt := ui.NewTester(a.syncModeSettings, 600, 200)
	clickInterface(t, tt, "手动")
	cfg, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || !cfg.Values.ManualSync || a.syncMode != 1 {
		t.Fatal("manual mode not saved", err)
	}
	a.cfg.Path = filepath.Dir(a.cfg.Path) // Cannot rename a settings file over a directory.
	clickInterface(t, tt, "自动")
	if a.syncMode != 1 || !a.cfg.Values.ManualSync || a.syncModeError == "" {
		t.Fatal("failed save changed mode")
	}
}
