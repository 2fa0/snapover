package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// TestUpdaterPluginRegisters keeps the plugin registration honest: Use
// panics on a plugin whose options or texts are wrong, which would take the
// app down before it opens. Other tests draw the update group without the
// plugin and never reach the updater it registers, so this runs once.
func TestUpdaterPluginRegisters(t *testing.T) {
	useUpdater()
	if got := mygo.App.Version(); got != "" {
		t.Fatalf("test binary reports version %q", got)
	}
}

func TestUpdateStateText(t *testing.T) {
	checked := time.Date(2026, 10, 20, 9, 5, 0, 0, time.Local)
	tests := []struct {
		name    string
		enabled bool
		last    time.Time
		want    string
	}{
		{"unsupported build", false, checked, "不能自动更新"},
		{"never checked", true, time.Time{}, "还没有检查过"},
		{"checked before", true, checked, "上次检查：2026-10-20 09:05"},
	}
	for _, tc := range tests {
		a := &app{updatesEnabled: tc.enabled, lastCheck: tc.last}
		if got := a.updateStateText(); !strings.Contains(got, tc.want) {
			t.Fatalf("%s: updateStateText() = %q, want it to contain %q", tc.name, got, tc.want)
		}
	}
}

func TestSettingsPageShowsUpdateGroup(t *testing.T) {
	a := interfaceTestApp(t)
	a.page = pageSettings
	tt := ui.NewTester(a.view, 960, 640)
	if !tt.HasText("关于与更新") {
		t.Fatal("settings page has no update group")
	}
	if !tt.HasText("当前版本 " + appVersionLabel()) {
		t.Fatal("update group does not show the version")
	}
	if !tt.HasText("这个版本不能自动更新") {
		t.Fatal("update group does not explain why a test build cannot update")
	}
	// A build that cannot update itself explains it instead of opening the
	// update window, which needs the plugin that only main registers.
	// The row is below the first screen of the settings page, so this calls
	// the action the button runs rather than clicking it.
	a.checkForUpdates()
	if !strings.Contains(a.status, "不能自动更新") {
		t.Fatalf("status = %q after checking for updates in a test build", a.status)
	}
}

func TestUpdateGroupHidesAutomaticChecksWhenUnsupported(t *testing.T) {
	a := interfaceTestApp(t)
	a.updatesEnabled = true
	a.page = pageSettings
	tt := ui.NewTester(a.view, 960, 640)
	if !tt.HasText("自动检查更新") {
		t.Fatal("supported build does not offer automatic checks")
	}

	a.updatesEnabled = false
	tt = ui.NewTester(a.view, 960, 640)
	if tt.HasText("自动检查更新") {
		t.Fatal("unsupported build offers automatic checks it cannot run")
	}
}
