package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/resources"
	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo/ui"
)

func TestPickerShortcuts(t *testing.T) {
	got := pickerShortcuts()
	if runtime.GOOS == "darwin" {
		if len(got) != 2 || got[0] != "Alt+Cmd+V" {
			t.Fatalf("darwin shortcuts = %v", got)
		}
		return
	}
	if len(got) != 1 || got[0] != "Alt+V" {
		t.Fatalf("shortcuts = %v", got)
	}
}

func TestMenuPNGBecomesTrayIcon(t *testing.T) {
	for selected := 1; selected <= 3; selected++ {
		for _, white := range []bool{false, true} {
			out, err := menuBarIconPNG(selected, white)
			if err != nil {
				t.Fatal(err)
			}
			checkMenuPNGBecomesTrayIcon(t, out)
		}
	}
}

func TestDefaultTrayIconUsesMailbox(t *testing.T) {
	if !bytes.Equal(menuIconSource(0), resources.Icon2PNG) {
		t.Fatal("removed icon did not fall back to the mailbox")
	}
	actual, err := (&app{}).menuIcon(true)
	if err != nil {
		t.Fatal(err)
	}
	want, err := menuBarIconPNG(2, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, want) {
		t.Fatal("default tray icon is not the mailbox")
	}
}

func TestTrayMenuKeepsOnlyActions(t *testing.T) {
	a := interfaceTestApp(t)
	a.status = "已通过局域网连接 2fa0-hp"
	var labels []string
	for _, item := range a.trayMenu().Items() {
		if item.Label == "" || !item.IsVisible() {
			continue
		}
		labels = append(labels, item.Label)
	}
	want := []string{"快速历史", "设置", "检查更新…", "暂停同步", "开机启动", "退出"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Fatalf("tray menu = %v, want %v", labels, want)
	}
}

func TestIgnoreNextCopySettingsAndTemporaryTrayCancel(t *testing.T) {
	a := interfaceTestApp(t)
	a.engine.Board = &pasteTestBoard{}
	tt := ui.NewTester(a.settingsPage, 960, 1200)
	if !tt.HasText("忽略一次") || a.trayMenu().ItemByID("cancel-ignore-next-copy").IsVisible() {
		t.Fatal("ignore action is missing from settings or cancel is visible while idle")
	}
	clickInterface(t, tt, "启用忽略下一次复制")
	tt.Frame()
	cancel := a.trayMenu().ItemByID("cancel-ignore-next-copy")
	if !a.engine.IgnoreNextCopyPending() || !tt.HasText("等待复制…") || !cancel.IsVisible() {
		t.Fatal("arming ignore did not expose the waiting state and tray cancellation")
	}
	item, err := clip.FromText("private test content")
	if err != nil {
		t.Fatal(err)
	}
	a.engine.HandleLocal(1, item)
	tt.Frame()
	if a.engine.IgnoreNextCopyPending() || a.trayMenu().ItemByID("cancel-ignore-next-copy").IsVisible() || !tt.HasText("忽略一次") {
		t.Fatal("settled copy did not remove cancellation and restore the settings action")
	}
	// A menu opened before the copy completed must only cancel, never re-arm.
	cancel.Click(cancel, nil)
	if a.engine.IgnoreNextCopyPending() {
		t.Fatal("stale cancellation re-armed ignore")
	}
	clickInterface(t, tt, "启用忽略下一次复制")
	cancel = a.trayMenu().ItemByID("cancel-ignore-next-copy")
	cancel.Click(cancel, nil)
	tt.Frame()
	if a.engine.IgnoreNextCopyPending() || a.trayMenu().ItemByID("cancel-ignore-next-copy").IsVisible() {
		t.Fatal("tray cancellation left ignore active")
	}
	clickInterface(t, tt, "启用忽略下一次复制")
	tt.Frame()
	clickInterface(t, tt, "取消忽略下一次复制")
	if a.engine.IgnoreNextCopyPending() || a.trayMenu().ItemByID("cancel-ignore-next-copy").IsVisible() {
		t.Fatal("settings cancellation left ignore active")
	}
}

func checkMenuPNGBecomesTrayIcon(t *testing.T, out []byte) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	var opaque int
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a > 0x2000 {
				opaque++
			}
		}
	}
	if opaque < 20 || opaque > 700 {
		t.Fatalf("opaque pixels = %d", opaque)
	}
}

func TestMenuBarIconSelectionPersists(t *testing.T) {
	a := interfaceTestApp(t)
	tt := ui.NewTester(a.menuBarIconSettings, 600, 180)
	if a.cfg.Values.MenuBarIcon != settings.DefaultMenuBarIcon || tt.HasText("原图标") {
		t.Fatal("icon chooser did not default to the mailbox or still offers the removed icon")
	}
	for _, selected := range []int{1, 3, 2} {
		label := []string{"", "小鸟", "邮筒", "信封"}[selected]
		clickInterface(t, tt, label)
		got, err := settings.Load(filepath.Dir(a.cfg.Path))
		if err != nil {
			t.Fatal(err)
		}
		if a.menuBarIconError != "" || got.Values.MenuBarIcon != selected {
			t.Fatalf("selection %d: saved=%d error=%q", selected, got.Values.MenuBarIcon, a.menuBarIconError)
		}
		// Both platforms restore the same saved artwork, rendered as a template.
		b := &app{cfg: got}
		actual, err := b.menuIcon(true)
		if err != nil {
			t.Fatal(err)
		}
		want, err := menuBarIconPNG(selected, false)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, want) {
			t.Fatalf("selection %d: wrong startup icon", selected)
		}
	}
	tt.Key(0, ui.KeyRight)
	got, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || got.Values.MenuBarIcon != 1 {
		t.Fatalf("keyboard selection was not saved: cfg=%+v err=%v", got, err)
	}
}

func TestMenuBarIconSaveFailureKeepsPreviousSelection(t *testing.T) {
	a := interfaceTestApp(t)
	a.saveMenuBarIcon(1)
	if a.menuBarIconError != "" {
		t.Fatal(a.menuBarIconError)
	}
	if err := os.Mkdir(a.cfg.Path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.menuBarIconSettings, 600, 180)
	clickInterface(t, tt, "信封")
	got, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil {
		t.Fatal(err)
	}
	if a.cfg.Values.MenuBarIcon != 1 || got.Values.MenuBarIcon != 1 || !tt.HasText(a.menuBarIconError) || a.menuBarIconError == "" {
		t.Fatalf("failed save changed selection or hid error: saved=%d active=%d error=%q", got.Values.MenuBarIcon, a.cfg.Values.MenuBarIcon, a.menuBarIconError)
	}
}

func TestTrayIconPreservesAlphaAndAspectRatio(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 100})
		}
	}
	for y := 1; y < 3; y++ {
		for x := 2; x < 6; x++ {
			pixel := color.NRGBA{A: 255}
			if x >= 4 {
				pixel = color.NRGBA{A: 128}
			}
			src.SetNRGBA(x, y, pixel)
		}
	}
	src.SetNRGBA(2, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	src.SetNRGBA(5, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 128})
	for _, white := range []bool{false, true} {
		out, err := renderTrayIconPNG(src, white, image.Rect(3, 3, 29, 29))
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds() != image.Rect(0, 0, 32, 32) {
			t.Fatalf("bounds = %v", img.Bounds())
		}
		if bounds := cropAlpha(img).Bounds(); bounds.Dx() != 26 || bounds.Dy() != 13 {
			t.Fatalf("bird bounds = %v, want 26x13", bounds)
		}
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Fatal("corner should be transparent")
		}
		for _, x := range []int{5, 24} {
			if _, _, _, a := img.At(x, 11).RGBA(); a != 0 {
				t.Fatal("white feather detail should be transparent")
			}
		}
		var ink uint8
		if white {
			ink = 255
		}
		for _, sample := range []struct {
			x     int
			alpha uint8
		}{{8, 255}, {23, 128}} {
			got := color.NRGBAModel.Convert(img.At(sample.x, 16)).(color.NRGBA)
			want := color.NRGBA{R: ink, G: ink, B: ink, A: sample.alpha}
			if got != want {
				t.Fatalf("white=%v pixel(%d,16) = %v, want %v", white, sample.x, got, want)
			}
		}
	}
}
