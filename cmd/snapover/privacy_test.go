package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo/ui"
)

func TestPrivacySettingsPersistAndTrayReflectsMode(t *testing.T) {
	a := interfaceTestApp(t)
	a.page = pageSettings
	tt := ui.NewTester(a.view, 960, 1000)
	clickInterface(t, tt, "隐私模式开关")
	a.excludedAppsText = " Safari ; 1Password； "
	a.savePrivacy()
	cfg, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || !cfg.Values.PrivacyMode || strings.Join(cfg.Values.ExcludedApps, ";") != "Safari;1Password" {
		t.Fatal("privacy settings not persisted", cfg, err)
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
