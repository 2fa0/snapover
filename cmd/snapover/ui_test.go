package main

import (
	"bytes"
	"image"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/settings"
	clipsync "github.com/2fa0/snapover/internal/sync"
)

func interfaceTestApp(t *testing.T) *app {
	t.Helper()
	dir := t.TempDir()
	store, err := history.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	peers, err := clipsync.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := settings.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	engine := &clipsync.Engine{Peers: peers, Name: "测试电脑"}
	engine.SetReceiveDir(dir)
	return newApp(engine, store, cfg, &identity.Identity{}, ":47821", nil, "")
}

func clickInterface(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	if err := tt.Click(text); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryFilterActionsKeepPinnedRecords(t *testing.T) {
	a := interfaceTestApp(t)
	text, _ := clip.FromText("保留的文本")
	pinned, err := a.hist.Add(history.DirLocal, "", text)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.hist.SetPinned(pinned.ID, true); err != nil {
		t.Fatal(err)
	}
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromPNG(pixels.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.hist.Add(history.DirRemote, "其他电脑", item); err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	a.page = pageHistory
	tt := ui.NewTester(a.view, 960, 640)
	clickInterface(t, tt, "图片")
	if len(a.shown()) != 1 || a.histSel != -1 {
		t.Fatal("filter should show only the image with no old selection")
	}
	clickInterface(t, tt, entryTitle(a.shown()[0]))
	clickInterface(t, tt, "删除")
	if len(a.shown()) != 0 || len(a.hist.List()) != 1 {
		t.Fatal("deleting a filtered image should leave the pinned text")
	}
	clickInterface(t, tt, "全部")
	clickInterface(t, tt, "保留的文本")
	if entry, ok := a.selected(); !ok || entry.ID != pinned.ID || !tt.HasText("取消置顶") {
		t.Fatal("selection or pin action did not follow the remaining text")
	}
	clickInterface(t, tt, "清空未置顶")
	clickInterface(t, tt, "确认清空")
	if entries := a.hist.List(); len(entries) != 1 || entries[0].ID != pinned.ID {
		t.Fatal("clearing should keep the pinned record")
	}
}

func TestSettingsControlsPersistPolicy(t *testing.T) {
	a := interfaceTestApp(t)
	a.page = pageSettings
	tt := ui.NewTester(a.view, 960, 640)
	clickInterface(t, tt, "暂停同步开关")
	clickInterface(t, tt, "只同步文本开关")
	clickInterface(t, tt, "图片大小上限，MB")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("8")
	clickInterface(t, tt, "应用")
	got, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Values.Paused || !got.Values.TextOnly || got.Values.MaxImageMB != 8 {
		t.Fatalf("settings were not saved: %+v", got.Values)
	}
}

func TestPairingCanCancelWithoutTrustingDevices(t *testing.T) {
	a := interfaceTestApp(t)
	tt := ui.NewTester(a.view, 960, 640)
	clickInterface(t, tt, "添加设备")
	if a.engine.PairingUntil().IsZero() || !tt.HasText("取消添加") {
		t.Fatal("adding did not open the pairing window")
	}
	clickInterface(t, tt, "取消添加")
	if !a.engine.PairingUntil().IsZero() || len(a.engine.Peers.List()) != 0 {
		t.Fatal("cancel should close pairing without trusting a device")
	}
}
