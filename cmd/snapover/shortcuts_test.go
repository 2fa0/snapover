package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo"
)

func TestPickerShortcutValidationAndAliases(t *testing.T) {
	for value, want := range map[string]string{
		" Alt + control + v ": "Ctrl+Alt+V",
		"option+command+V":    "Super+Alt+V",
		"Win+Shift+f12":       "Super+Shift+F12",
		"Ctrl+Space":          "Ctrl+Space",
		"Alt+PageDown":        "Alt+PageDown",
		"Alt+F":               "Alt+F",
	} {
		got, err := normalizePickerShortcut(value)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", value, got, err)
		}
	}
	for _, value := range []string{"", "V", "Shift+V", "Ctrl+", "Ctrl+Ctrl+V", "Alt+Option+V", "Unknown+V", "Ctrl+中文", "Ctrl+F13", "Ctrl+F01", "Ctrl+A+B"} {
		if _, err := normalizePickerShortcut(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	defaults, err := configuredPickerShortcuts("")
	if err != nil {
		t.Fatal(err)
	}
	want := 1
	if runtime.GOOS == "darwin" {
		want = 2
	}
	if len(defaults) != want {
		t.Fatalf("lost default shortcut: %v", defaults)
	}
}

func TestShortcutChangesKeepOldKeysOnRegistrationOrSaveFailure(t *testing.T) {
	for _, failure := range []string{"occupied", "save", "none"} {
		t.Run(failure, func(t *testing.T) {
			registered := map[string]bool{"Alt+V": true}
			saved := false
			err := changePickerShortcuts([]string{"Alt+V"}, []string{"Ctrl+Alt+V", "Ctrl+Shift+V"},
				func(key string) error {
					if failure == "occupied" && key == "Ctrl+Shift+V" {
						return errors.New("occupied")
					}
					registered[key] = true
					return nil
				}, func(key string) { delete(registered, key) }, func() error {
					if !registered["Alt+V"] {
						t.Fatal("old key was removed before commit")
					}
					if failure == "save" {
						return errors.New("permission denied")
					}
					saved = true
					return nil
				})
			if failure == "none" {
				if err != nil || !saved || registered["Alt+V"] || len(registered) != 2 {
					t.Fatalf("change not committed: %v %+v", err, registered)
				}
			} else if err == nil || saved || len(registered) != 1 || !registered["Alt+V"] {
				t.Fatalf("failed change replaced old keys: %v %+v", err, registered)
			}
		})
	}
}

func TestShortcutReusingExistingDefaultDoesNotReregister(t *testing.T) {
	err := changePickerShortcuts([]string{"Super+Alt+V", "Super+Shift+V"}, []string{"Super+Alt+V"},
		func(string) error { t.Fatal("registered a key already owned"); return nil },
		func(key string) {
			if key != "Super+Shift+V" {
				t.Fatal("removed retained key")
			}
		}, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
}

func TestPickerShortcutNativeChangeKeepsWorkingKeyOnConflict(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	a := interfaceTestApp(t)
	oldKey, newKey := "Ctrl+Alt+Shift+F11", "Ctrl+Alt+Shift+F12"
	var registrationErr error
	mygo.RunOnMain(func() { registrationErr = mygo.GlobalShortcut.Register(oldKey, func() {}) })
	if registrationErr != nil {
		t.Skipf("test shortcut unavailable: %v", registrationErr)
	}
	t.Cleanup(func() {
		mygo.RunOnMain(func() { mygo.GlobalShortcut.Unregister(oldKey); mygo.GlobalShortcut.Unregister(newKey) })
	})
	mygo.RunOnMain(func() {
		a.registeredPickerShortcuts = []string{oldKey}
		a.cfg.Values.PickerShortcut = oldKey
		registrationErr = mygo.GlobalShortcut.Register(newKey, func() {})
	})
	if registrationErr != nil {
		t.Skipf("second test shortcut unavailable: %v", registrationErr)
	}
	mygo.RunOnMain(func() {
		a.savePickerShortcut(newKey)
		if a.shortcutError == "" || a.cfg.Values.PickerShortcut != oldKey || !mygo.GlobalShortcut.IsRegistered(oldKey) {
			t.Error("occupied shortcut replaced the working key")
		}
		mygo.GlobalShortcut.Unregister(newKey)
		a.savePickerShortcut(newKey)
		if a.shortcutError != "" || mygo.GlobalShortcut.IsRegistered(oldKey) || !mygo.GlobalShortcut.IsRegistered(newKey) {
			t.Error("native shortcut replacement failed", a.shortcutError)
		}
	})
	restored, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || restored.Values.PickerShortcut != newKey {
		t.Fatalf("shortcut not persisted: %+v %v", restored, err)
	}
}

func TestDisplayShortcutUsesPlatformModifierNames(t *testing.T) {
	// Stored values, defaults included, spell the command key several ways.
	cases := map[string][2]string{
		"Super+Alt+C":  {"Alt+Win+C", "⌥+⌘+C"},
		"Ctrl+Alt+C":   {"Ctrl+Alt+C", "⌃+⌥+C"},
		"Alt+Cmd+V":    {"Alt+Win+V", "⌥+⌘+V"},
		"Cmd+Shift+V":  {"Shift+Win+V", "⇧+⌘+V"},
		"Alt+V":        {"Alt+V", "⌥+V"},
		"Ctrl+Shift+P": {"Ctrl+Shift+P", "⌃+⇧+P"},
	}
	for key, want := range cases {
		expected := want[0]
		if runtime.GOOS == "darwin" {
			expected = want[1]
		}
		if got := displayPickerShortcut(key); got != expected {
			t.Fatalf("displayPickerShortcut(%q) = %q, want %q", key, got, expected)
		}
	}
}

func TestModifierHintsUsePlatformKeyNames(t *testing.T) {
	invalid, missing := modifierHints()
	wantInvalid, wantMissing := "修饰键请使用 Ctrl、Alt、Shift、Cmd 或 Win", "请至少使用 Ctrl、Alt、Cmd 或 Win 中的一个修饰键"
	if runtime.GOOS == "darwin" {
		wantInvalid, wantMissing = "修饰键请使用 ⌃、⌥、⇧ 或 ⌘", "请至少使用 ⌃、⌥ 或 ⌘ 中的一个修饰键"
	}
	if invalid != wantInvalid || missing != wantMissing {
		t.Fatalf("modifierHints = %q, %q; want %q, %q", invalid, missing, wantInvalid, wantMissing)
	}
	for _, text := range []string{invalid, missing} {
		if en := translateEN(text); en == text {
			t.Fatalf("%q has no English translation", text)
		}
	}
}
