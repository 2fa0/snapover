package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/keepalive"
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

func TestMainNavigationRemainsImmediateDuringMotion(t *testing.T) {
	for _, reduceMotion := range []bool{false, true} {
		a := interfaceTestApp(t)
		tt := ui.NewTester(a.view, 960, 640)
		tt.SetPreferences(ui.Preferences{ReduceMotion: reduceMotion})
		for _, target := range []struct{ label, page string }{
			{"设置", pageSettings},
			{"复制历史", pageHistory},
			{"连接设备", pageDevices},
			{"设置", pageSettings},
		} {
			clickInterface(t, tt, target.label)
			if a.page != target.page {
				t.Fatalf("navigation during animation chose %s, want %s", a.page, target.page)
			}
		}
		tt.Key(0, ui.KeyDown)
		if a.page != pageDevices {
			t.Fatal("sidebar keyboard navigation stopped working")
		}
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
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	clickInterface(t, tt, "暂停同步开关")
	clickInterface(t, tt, "只同步文本开关")
	if tt.HasText("图片大小上限，MB") || tt.HasText("文本大小上限，MB") {
		t.Fatal("size limits should stay collapsed until requested")
	}
	clickInterface(t, tt, "大小限制")
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

func TestTextLimitInputAppliesAndRejectsInvalidValues(t *testing.T) {
	a := interfaceTestApp(t)
	tt := ui.NewTester(a.settingsPage, 960, 1200)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	clickInterface(t, tt, "大小限制")
	clickInterface(t, tt, "文本大小上限，MB")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("8")
	clickInterface(t, tt, "应用文本上限")
	got, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || got.Values.MaxTextMB != 8 || a.engine.TextLimit() != 8<<20 {
		t.Fatalf("text limit was not applied: %+v %v", got, err)
	}
	for _, value := range []string{"", "abc", "0", "-1", "21", "1.5"} {
		a.maxTextText = value
		a.saveTextLimit()
		if a.maxTextError == "" || a.cfg.Values.MaxTextMB != 8 || a.engine.TextLimit() != 8<<20 {
			t.Fatalf("invalid limit %q changed the setting", value)
		}
	}
	a.maxTextText = " 20 "
	a.saveTextLimit()
	if a.maxTextError != "" || a.maxTextText != "20" || a.engine.TextLimit() != 20<<20 {
		t.Fatal("valid input did not clear the validation error")
	}
	reopened, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil {
		t.Fatal(err)
	}
	b := newApp(a.engine, a.hist, reopened, &identity.Identity{}, ":47821", nil, "")
	if b.maxTextText != "20" || b.engine.TextLimit() != 20<<20 {
		t.Fatal("saved limit was not restored on startup")
	}
}

func TestTextLimitSaveFailureKeepsActiveLimit(t *testing.T) {
	a := interfaceTestApp(t)
	// A directory at the settings file path prevents publishing the new value.
	if err := os.Mkdir(a.cfg.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	a.maxTextText = "8"
	a.saveTextLimit()
	if a.maxTextError == "" || a.cfg.Values.MaxTextMB != 1 || a.engine.TextLimit() != 1<<20 {
		t.Fatal("failed save changed the active limit")
	}
}

func TestKeepRemoteActiveSettingIsOptionalAndPersists(t *testing.T) {
	a := interfaceTestApp(t)
	if a.cfg.Values.KeepRemoteActive {
		t.Fatal("remote session activity must start disabled")
	}
	tt := ui.NewTester(a.settingsPage, 960, 1200)
	if tt.HasText("保持远程会话活跃") != keepalive.Supported {
		t.Fatal("remote session switch should only be shown on Windows")
	}
	if keepalive.Supported {
		clickInterface(t, tt, "保持远程会话活跃开关")
	} else {
		a.setKeepRemoteActive(true)
	}
	got, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || !got.Values.KeepRemoteActive {
		t.Fatalf("enabled preference was not saved: %v", err)
	}
	if keepalive.Supported {
		clickInterface(t, tt, "保持远程会话活跃开关")
	} else {
		a.setKeepRemoteActive(false)
	}
	got, err = settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || got.Values.KeepRemoteActive {
		t.Fatalf("disabled preference was not saved: %v", err)
	}
}

func TestKeepRemoteActiveSaveFailureDoesNotEnablePreference(t *testing.T) {
	a := interfaceTestApp(t)
	a.cfg.Path = filepath.Dir(a.cfg.Path)
	a.setKeepRemoteActive(true)
	if a.cfg.Values.KeepRemoteActive || a.status != "保存远程会话设置失败，请重试" {
		t.Fatal("failed opt-in should remain disabled and report the failure")
	}
	a.cfg.Values.KeepRemoteActive = true
	a.remoteActivity.SetEnabled(true)
	a.setKeepRemoteActive(false)
	if a.cfg.Values.KeepRemoteActive {
		t.Fatal("failed save must not turn the switch back on after disabling")
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

func TestPairedDevicesRevealSearchOnlyWhenAdding(t *testing.T) {
	a := interfaceTestApp(t)
	if _, err := a.engine.Peers.Allow("paired", "fingerprint", "工作电脑", "windows", "192.168.1.20:47821"); err != nil {
		t.Fatal(err)
	}
	a.devices = a.engine.Peers.List()
	tt := ui.NewTester(a.view, 960, 640)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if !tt.HasText("工作电脑") || tt.HasText("附近的设备") || tt.HasText("对端局域网地址") {
		t.Fatal("paired devices should take priority over search and address input")
	}
	clickInterface(t, tt, "通过地址连接")
	if !tt.HasText("对端局域网地址") {
		t.Fatal("manual connection should remain available on demand")
	}
	clickInterface(t, tt, "添加设备")
	if !tt.HasText("附近的设备") || !tt.HasText("两台电脑连接同一网络") {
		t.Fatal("adding should reveal discovery and the pairing instructions")
	}
	clickInterface(t, tt, "取消添加")
	if tt.HasText("附近的设备") {
		t.Fatal("canceling should return to the paired device list")
	}
	a.discoveryMessage = "搜索失败，请检查本地网络权限"
	tt.Frame()
	if !tt.HasText(a.discoveryMessage) {
		t.Fatal("discovery errors should remain visible outside the adding flow")
	}
}

func TestNetworkPermissionReminderAppearsWhenAdding(t *testing.T) {
	previous := currentNetworkGuideKind
	t.Cleanup(func() { currentNetworkGuideKind = previous })
	currentNetworkGuideKind = guideMacLocalNetwork
	a := interfaceTestApp(t)
	tt := ui.NewTester(a.view, 960, 640)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if tt.HasText("查看连接指引") {
		t.Fatal("permission instructions should wait until the user adds a device")
	}
	clickInterface(t, tt, "添加设备")
	if !tt.HasText("查看连接指引") || !tt.HasText("本地网络权限") {
		t.Fatal("adding should explain the network permission before pairing")
	}
	clickInterface(t, tt, "取消添加")
	if tt.HasText("查看连接指引") || !tt.HasText("连接帮助") {
		t.Fatal("canceling should hide the reminder and preserve manual help")
	}
}
