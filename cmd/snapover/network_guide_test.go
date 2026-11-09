package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/discovery"
	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestNetworkGuideShowsHelpAfterEmptySearchWithoutClaimingFirewallFailure(t *testing.T) {
	a := interfaceTestApp(t)
	g := &networkGuide{app: a, kind: guideWindows, searchStarted: time.Now()}
	tt := ui.NewTester(g.view, 600, 1400)
	if !tt.HasText("正在搜索同一网络内的电脑…") || g.showSettings {
		t.Fatal("initial search should not show a failure or expand troubleshooting")
	}
	g.searchStarted = time.Now().Add(-networkGuideSearchDelay)
	tt = ui.NewTester(g.view, 600, 1400)
	if !tt.HasText("暂未发现其他电脑") || !tt.HasText("打开防火墙设置") {
		t.Fatal("empty search should offer actionable firewall help")
	}
	clickInterface(t, tt, "查看防火墙设置方法")
	if g.showSettings {
		t.Fatal("automatically expanded help should remain collapsible")
	}
	a.discovered = []discovery.Device{{ID: "found", Name: "另一台电脑"}}
	tt = ui.NewTester(g.view, 600, 1400)
	if !tt.HasText("已发现 1 台电脑") || a.engine.Connected() {
		t.Fatal("discovery should report a found computer without treating it as connected")
	}
}

func TestNetworkGuideDismissalPersistsAndSaveFailureCanRetry(t *testing.T) {
	for _, kind := range []networkGuideKind{guideWindows, guideMacLocalNetwork} {
		a := interfaceTestApp(t)
		path := a.cfg.Path
		a.cfg.Path = filepath.Dir(path)
		a.dismissNetworkGuide(kind)
		if *networkGuideDismissed(&a.cfg.Values, kind) || a.status == "" {
			t.Fatal("save failure should preserve the reminder and provide a recovery action")
		}
		a.cfg.Path = path
		a.dismissNetworkGuide(kind)
		cfg, err := settings.Load(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if !*networkGuideDismissed(&cfg.Values, kind) || cfg.Values.WindowsNetworkGuideDismissed == cfg.Values.MacNetworkGuideDismissed {
			t.Fatal("dismissal should survive a restart without dismissing another platform's guide")
		}
	}
}

func TestMacNetworkGuideVersionGatesLocalNetworkInstructions(t *testing.T) {
	for _, tc := range []struct {
		version string
		kind    networkGuideKind
	}{
		{"13.6.9", guideMacLegacy},
		{"14.7", guideMacLegacy},
		{"15.0", guideMacLocalNetwork},
		{"26.1", guideMacLocalNetwork},
		{"", guideMacLegacy},
	} {
		kind := macNetworkGuideKind(tc.version)
		if kind != tc.kind {
			t.Fatalf("version %q: got %v, want %v", tc.version, kind, tc.kind)
		}
		a := interfaceTestApp(t)
		g := &networkGuide{app: a, kind: kind, searchStarted: time.Now().Add(-time.Minute)}
		tt := ui.NewTester(g.view, 600, 1400)
		if !tt.HasText("暂未发现其他电脑") || tt.HasText("打开本地网络设置") != (kind == guideMacLocalNetwork) {
			t.Fatalf("version %q has incorrect local network instructions", tc.version)
		}
		if tt.HasText("打开防火墙设置") || tt.HasText("辅助功能") || tt.HasText("权限被拒绝") {
			t.Fatal("Mac connection help should not request unrelated permissions or infer a denial")
		}
	}
}

func TestMacNetworkGuideReminderCanBeDismissedWithoutLoadingWindow(t *testing.T) {
	previous := currentNetworkGuideKind
	t.Cleanup(func() { currentNetworkGuideKind = previous })
	a := interfaceTestApp(t)
	currentNetworkGuideKind = guideMacLegacy
	tt := ui.NewTester(a.networkSetupHint, 600, 260)
	if tt.HasText("查看连接指引") || tt.HasText("打开本地网络设置") {
		t.Fatal("older Mac versions should not show the permission reminder")
	}
	currentNetworkGuideKind = guideMacLocalNetwork
	tt = ui.NewTester(a.networkSetupHint, 600, 260)
	if !tt.HasText("允许 Snapover 查找另一台电脑") {
		t.Fatal("first setup should explain local network access")
	}
	clickInterface(t, tt, "不再提示")
	if tt.HasText("打开本地网络设置") || a.networkGuide != nil || !a.cfg.Values.MacNetworkGuideDismissed {
		t.Fatal("dismissal should remove the reminder without allocating a guide window")
	}
	cfg, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || !cfg.Values.MacNetworkGuideDismissed {
		t.Fatalf("Mac reminder dismissal was not saved: %v", err)
	}
	tt = ui.NewTester(a.networkHelp, 600, 100)
	if !tt.HasText("连接帮助") {
		t.Fatal("dismissal should preserve the manual help entry")
	}
}

func TestNetworkSetupHintOffersSystemPermissionWithoutGuide(t *testing.T) {
	previous := currentNetworkGuideKind
	t.Cleanup(func() { currentNetworkGuideKind = previous })
	for _, tc := range []struct {
		kind   networkGuideKind
		action string
	}{
		{guideWindows, "打开防火墙设置"},
		{guideMacLocalNetwork, "打开本地网络设置"},
	} {
		currentNetworkGuideKind = tc.kind
		a := interfaceTestApp(t)
		tt := ui.NewTester(a.networkSetupHint, 600, 260)
		if !tt.HasText(tc.action) || !tt.HasText("连接帮助") || tt.HasText("查看连接指引") || a.networkGuide != nil {
			t.Fatalf("kind %v should offer %s without opening the guide", tc.kind, tc.action)
		}
	}
}
func TestNetworkGuideRetryUsesExistingDiscoveryAndDoesNotBlock(t *testing.T) {
	a := interfaceTestApp(t)
	a.discoveryWake = make(chan struct{}, 1)
	g := &networkGuide{app: a, searchStarted: time.Now().Add(-time.Minute)}
	g.retry()
	g.retry()
	if len(a.discoveryWake) != 1 || time.Since(g.searchStarted) > time.Second {
		t.Fatal("retry should wake the existing scanner and refresh the search phase")
	}
}

func TestNetworkGuideWindowReleasesStateAndCanBeRecreated(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	a := interfaceTestApp(t)
	a.discoveryWake = make(chan struct{}, 1)
	mygo.RunOnMain(func() {
		baseline := len(mygo.Windows())
		for range 3 {
			a.openNetworkGuide()
			g := a.networkGuide
			win, ctx := g.win, g.ctx
			a.openNetworkGuide()
			if a.networkGuide != g || len(mygo.Windows()) != baseline+1 {
				t.Error("opening an existing guide created a duplicate window")
			}
			g.showSettings = true
			g.message = "temporary settings error"
			if _, err := win.CapturePage(); err != nil {
				t.Errorf("render guide: %v", err)
			}
			win.Close()
			if !win.IsDestroyed() || a.networkGuide != nil || len(mygo.Windows()) != baseline {
				t.Error("closing retained the guide's native window or owner reference")
			}
			if ctx.Err() != context.Canceled || g.app != nil || g.win != nil || g.ctx != nil || g.cancel != nil || g.message != "" || g.showSettings {
				t.Error("closing retained the guide's context, callbacks or temporary UI state")
			}
		}
	})
	cfg, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || !*networkGuideDismissed(&cfg.Values, currentNetworkGuideKind) {
		t.Fatalf("closing did not dismiss the first-use reminder: %v", err)
	}
}
