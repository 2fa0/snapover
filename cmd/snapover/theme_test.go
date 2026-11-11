package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestThemeSettingsSwitchAndPersist(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	previous := mygo.Theme.Source()
	t.Cleanup(func() { mygo.Theme.SetSource(previous) })
	a := interfaceTestApp(t)
	a.cfg.Values.Language = settings.LangZH
	tester := ui.NewTester(a.themeSettings, 700, 160)
	tester.Frame()
	for _, tc := range []struct {
		label  string
		theme  string
		source mygo.ThemeSource
	}{
		{"深色", settings.ThemeDark, mygo.ThemeDark},
		{"浅色", settings.ThemeLight, mygo.ThemeLight},
		{"跟随系统", settings.ThemeAuto, mygo.ThemeSystem},
	} {
		clickInterface(t, tester, tc.label)
		if got := mygo.Theme.Source(); got != tc.source {
			t.Fatalf("source = %q, want %q", got, tc.source)
		}
		if tc.theme != settings.ThemeAuto && mygo.Theme.IsDark() != (tc.theme == settings.ThemeDark) {
			t.Fatalf("native appearance does not match %q", tc.theme)
		}
		saved, err := settings.Load(filepath.Dir(a.cfg.Path))
		if err != nil {
			t.Fatal(err)
		}
		if saved.Values.Theme != tc.theme {
			t.Fatalf("saved theme = %q, want %q", saved.Values.Theme, tc.theme)
		}
		configureTheme(saved.Values.Theme)
		if got := mygo.Theme.Source(); got != tc.source {
			t.Fatalf("restored source = %q, want %q", got, tc.source)
		}
	}
}

func TestThemeSaveFailureKeepsPreviousChoice(t *testing.T) {
	a := interfaceTestApp(t)
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Path = filepath.Join(blocked, "settings.json")
	previous := mygo.Theme.Source()
	a.saveThemeChoice(2)
	if a.cfg.Values.Theme != settings.ThemeAuto || a.themeError == "" {
		t.Fatalf("failed save changed choice or lost error: %q, %q", a.cfg.Values.Theme, a.themeError)
	}
	if mygo.Theme.Source() != previous {
		t.Fatal("failed save changed the active theme")
	}
}
