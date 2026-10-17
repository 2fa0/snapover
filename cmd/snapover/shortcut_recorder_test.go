package main

import (
	"github.com/egoist/mygo"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestShortcutRecorderCapturesWithoutTypingAndOnlySavesOnRequest(t *testing.T) {
	a := &app{}
	current, failure := "Ctrl+Alt+V", ""
	saved := 0
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Children(func() {
			a.shortcutRecorder(c, "picker", "测试快捷键", current, &failure, func(key string) { saved++; current = key })
			ui.Button(c, "其他设置")
		})
	}, 600, 160)
	clickInterface(t, tt, "设置测试快捷键")
	tt.Key(ui.Ctrl|ui.Alt, ui.KeyK)
	if a.shortcutRecording == nil || a.shortcutRecording.key != "Ctrl+Alt+K" || saved != 0 || current != "Ctrl+Alt+V" {
		t.Fatal("key was not recorded as a draft")
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("保存测试快捷键") {
		t.Fatal("Save is not reachable by keyboard")
	}
	tt.Key(0, ui.KeyEnter)
	if saved != 1 || current != "Ctrl+Alt+K" || a.shortcutRecording != nil {
		t.Fatal("keyboard save failed")
	}
	for _, cancel := range []string{"escape", "escape-after-tab", "outside", "button", "blur"} {
		clickInterface(t, tt, "设置测试快捷键")
		tt.Key(ui.Ctrl, ui.KeyJ)
		switch cancel {
		case "escape":
			tt.Key(0, ui.KeyEscape)
		case "escape-after-tab":
			tt.Key(0, ui.KeyTab)
			tt.Key(0, ui.KeyEscape)
		case "outside":
			clickInterface(t, tt, "其他设置")
		case "button":
			clickInterface(t, tt, "取消设置测试快捷键")
		case "blur":
			tt.SetFocused(false)
			tt.SetFocused(true)
		}
		if a.shortcutRecording != nil || saved != 1 || current != "Ctrl+Alt+K" {
			t.Fatalf("%s did not cancel", cancel)
		}
	}
}

func TestShortcutRecorderRejectsBareKeysAndPreservesFailedDraft(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(func(c *ui.Context) {
		a.shortcutRecorder(c, "copy", "复制并同步快捷键", "Ctrl+Alt+C", &a.copyShortcutError, func(string) { a.copyShortcutError = "快捷键被占用" })
	}, 600, 160)
	clickInterface(t, tt, "设置复制并同步快捷键")
	tt.Key(0, ui.KeyK)
	if a.shortcutRecording.key != "" || a.copyShortcutError == "" {
		t.Fatal("accepted a bare key")
	}
	tt.Key(ui.Ctrl, ui.KeyC)
	if a.shortcutRecording.key != "" || a.copyShortcutError == "" {
		t.Fatal("accepted ordinary copy")
	}
	tt.Key(ui.Super|ui.Alt, ui.KeyF8)
	if a.shortcutRecording.key != "Super+Alt+F8" || a.copyShortcutError != "" {
		t.Fatal("valid chord was not captured")
	}
	clickInterface(t, tt, "保存复制并同步快捷键")
	if a.shortcutRecording == nil || a.copyShortcutError != "快捷键被占用" {
		t.Fatal("failed registration discarded draft")
	}
}

func TestRecordingRegisteredShortcutDoesNotInvokeItsAction(t *testing.T) {
	a := &app{}
	if a.captureRegisteredShortcut("Alt+V") {
		t.Fatal("swallowed shortcut outside recording")
	}
	a.beginShortcutRecording("picker")
	if !a.captureRegisteredShortcut("Alt+V") || a.shortcutRecording.key != "Alt+V" {
		t.Fatal("registered shortcut escaped recording")
	}
	a.beginShortcutRecording("copy")
	if a.shortcutRecording.kind != "copy" || a.shortcutRecording.key != "" {
		t.Fatal("old draft leaked into another setting")
	}
	a.cancelShortcutRecording()
	if a.captureRegisteredShortcut("Alt+V") {
		t.Fatal("shortcut stayed suppressed")
	}
}

func TestShortcutRecorderPhysicalKeys(t *testing.T) {
	for _, tt := range []struct {
		event ui.InputEvent
		want  string
	}{
		{ui.InputEvent{Key: ui.KeyUnknown, PhysicalKey: ui.KeyK, Mods: ui.Ctrl | ui.Alt}, "Ctrl+Alt+K"},
		{ui.InputEvent{Key: ui.Key2, Mods: ui.Super | ui.Shift}, "Super+Shift+2"},
		{ui.InputEvent{Key: ui.KeyLeft, Mods: ui.Alt}, "Alt+Left"},
	} {
		got, err := shortcutFromInput(tt.event)
		if err != nil || got != tt.want {
			t.Fatalf("got %q %v, want %q", got, err, tt.want)
		}
	}
}

func TestShortcutRecorderNativeMenuRestoredAfterCancelAndClose(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	a := interfaceTestApp(t)
	mygo.RunOnMain(func() {
		previous := mygo.App.Menu()
		defer mygo.App.SetMenu(previous)
		menu := mygo.NewMenu([]*mygo.MenuItem{{Label: "测试", Accelerator: "CmdOrCtrl+2"}})
		mygo.App.SetMenu(menu)
		a.reveal(pageSettings)
		a.beginShortcutRecording("picker")
		if mygo.App.Menu() != nil {
			t.Error("menu accelerators still intercept recording")
		}
		a.cancelShortcutRecording()
		if mygo.App.Menu() != menu {
			t.Error("cancel did not restore menu")
		}
		a.beginShortcutRecording("copy")
		a.win.Close()
		if a.shortcutRecording != nil || mygo.App.Menu() != menu {
			t.Error("closing window left recording or menu suspended")
		}
	})
}
