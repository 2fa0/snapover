package main

import (
	"bytes"
	"image"
	"image/png"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/apps"
	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo/ui"
)

func TestPrivacySettingsPersistAndTrayReflectsMode(t *testing.T) {
	a := interfaceTestApp(t)
	a.page = pageSettings
	tt := ui.NewTester(a.view, 960, 1000)
	clickInterface(t, tt, "隐私模式开关")
	a.addApplication(settings.ExcludedApp{ID: "com.apple.Safari", Name: "Safari"})
	a.addApplication(settings.ExcludedApp{ID: "com.1password.1password", Name: "1Password"})
	a.addApplication(settings.ExcludedApp{ID: "com.apple.Safari", Name: "Safari"})
	a.excludedWhitelist = true
	a.savePrivacy()
	cfg, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || !cfg.Values.PrivacyMode || !cfg.Values.ExcludedWhitelist || len(cfg.Values.ExcludedApps) != 2 {
		t.Fatal("privacy settings not persisted", cfg, err)
	}
	if entry := cfg.Values.ExcludedApps[0]; entry.Name != "Safari" || entry.ID != "com.apple.Safari" {
		t.Fatalf("entry not persisted with its identifier: %+v", entry)
	}
	if !a.trayMenu().ItemByID("privacy-mode").Checked {
		t.Fatal("tray privacy mode not checked")
	}
	if !strings.Contains(a.displayStatus(), "隐私模式") {
		t.Fatal("mode missing from visible status")
	}
}

func TestDiagnosticSummaryDoesNotExposeRawErrors(t *testing.T) {
	a := interfaceTestApp(t)
	a.syncFailure = "写入失败 secret-body /Users/private/report.pdf 192.168.2.3"
	summary := a.buildDiagnosticSummary()
	for _, secret := range []string{"secret-body", "/Users/private", "report.pdf", "192.168.2.3"} {
		if strings.Contains(summary, secret) {
			t.Fatal("diagnostic summary leaked raw data", secret)
		}
	}
	if !strings.Contains(summary, "失败或未确认") {
		t.Fatal("failure not categorized")
	}
	a.syncFailure, a.syncStatus = "", "已确认写入对端剪贴板"
	if !strings.Contains(a.buildDiagnosticSummary(), "对端已确认写入剪贴板") {
		t.Fatal("confirmation not recognized")
	}
}

func TestNewControlsTranslateToEnglish(t *testing.T) {
	a := interfaceTestApp(t)
	a.cfg.Values.Language = settings.LangEN
	for _, label := range []string{"隐私模式", "排除来源应用", "置顶备份", "生成诊断摘要", "文件已失效，请重新复制后再置顶"} {
		if a.tr(label) == label {
			t.Fatalf("untranslated label %s", label)
		}
	}
}

func TestApplicationListSwitchesOffAndRemovesRows(t *testing.T) {
	a := interfaceTestApp(t)
	a.page = pageSettings
	a.showPrivacy = true
	a.addApplication(settings.ExcludedApp{ID: "com.apple.Safari", Name: "Safari"})
	a.addApplication(settings.ExcludedApp{ID: "com.1password.1password", Name: "1Password"})
	tt := ui.NewTester(a.view, 960, 1200)
	tt.Frame()
	clickInterface(t, tt, "Safari switch")
	cfg, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || len(cfg.Values.ExcludedApps) != 2 || !cfg.Values.ExcludedApps[0].Off {
		t.Fatalf("switch not saved: %+v %v", cfg, err)
	}
	// The engine keeps the row without letting it decide anything.
	if len(privacyRules(cfg.Values)) != 1 {
		t.Fatalf("switched-off row still rules: %+v", privacyRules(cfg.Values))
	}
	tt.Frame()
	if err := tt.Click("移除"); err != nil {
		t.Fatal(err)
	}
	cfg, err = settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || len(cfg.Values.ExcludedApps) != 1 || cfg.Values.ExcludedApps[0].Name != "1Password" {
		t.Fatalf("removal not saved: %+v %v", cfg.Values.ExcludedApps, err)
	}
}

// testIconPNG is an 8 by 8 image, enough to stand in for an app icon.
func testIconPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewNRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestRunningAppMenuAddsTheAppOnce(t *testing.T) {
	a := interfaceTestApp(t)
	a.page = pageSettings
	a.showPrivacy = true
	icon := testIconPNG(t)
	a.runningApps = []apps.App{
		{Name: "Safari", ID: "com.apple.Safari", Icon: icon},
		{Name: "1Password", ID: "com.1password.1password", Icon: icon},
	}
	for _, entry := range a.runningApps {
		a.rememberIcon(entry)
	}
	tt := ui.NewTester(a.view, 960, 1200)
	tt.Frame()
	clickInterface(t, tt, "添加应用")
	tt.Frame()
	clickInterface(t, tt, "运行中的应用程序")
	tt.Frame()
	clickInterface(t, tt, "Safari")
	if len(a.cfg.Values.ExcludedApps) != 1 || a.cfg.Values.ExcludedApps[0].ID != "com.apple.Safari" {
		t.Fatalf("app not added: %+v", a.cfg.Values.ExcludedApps)
	}
	// The row shows the icon the app was added with.
	tt.Frame()
	if a.applicationIcon("com.apple.Safari", "Safari") == nil {
		t.Fatal("row has no icon")
	}
	// A listed app is offered as chosen, and choosing it again adds nothing.
	clickInterface(t, tt, "添加应用")
	tt.Frame()
	clickInterface(t, tt, "运行中的应用程序")
	tt.Frame()
	if err := tt.Click("Safari"); err != nil {
		t.Fatal(err)
	}
	if len(a.cfg.Values.ExcludedApps) != 1 {
		t.Fatalf("duplicate row: %+v", a.cfg.Values.ExcludedApps)
	}
}
