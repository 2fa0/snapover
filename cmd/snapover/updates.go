package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	updaterui "github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

// updateInterval is how often the app looks for a new version in the
// background. A day is enough for an app that mostly sits in the tray, and
// keeps the checks off the feed's back.
const updateInterval = 24 * time.Hour

// useUpdater registers the update plugin, which must happen before App.Run.
// Its preferences are first loaded by startUpdates, after PathUserData is set.
func useUpdater() {
	mygo.Use(updaterui.New(updater.Options{Interval: updateInterval}))
}

// startUpdates reads what the settings page shows and keeps it in step with
// the plugin, which changes the same values from its own window: the user
// answering a prompt, or asking for a check.
func (a *app) startUpdates() {
	a.updatesEnabled = mygo.Updater.Enabled()
	a.readUpdateState()
	updater.OnChange(func() {
		a.readUpdateState()
		if a.win != nil {
			a.win.Invalidate()
		}
	})
}

// readUpdateState copies the plugin's choices. The settings page reads these
// instead of the plugin, so that a test or a build without the plugin can
// draw it.
func (a *app) readUpdateState() {
	a.autoUpdate = updater.AutomaticChecks()
	a.lastCheck = updater.LastCheck()
}

// checkForUpdates opens the update window the user asked for. A build that
// cannot update itself, such as a development build or an app run from a
// read-only image, says so instead of opening a window that would fail.
func (a *app) checkForUpdates() {
	if !a.updatesEnabled {
		a.status = "这个版本不能自动更新，请从发布页下载新版"
		a.refreshTray()
		return
	}
	updater.CheckForUpdates()
}

// updateSettings is the settings page's "关于与更新" group.
func (a *app) updateSettings(c *ui.Context) {
	settingRow(c, a.tr(fmt.Sprintf("当前版本 %s", appVersionLabel())), a.tr(a.updateStateText()), func() {
		if ui.Button(c, a.tr("检查更新")).Clicked() {
			a.checkForUpdates()
		}
	})
	if !a.updatesEnabled {
		return
	}
	ui.Divider(c)
	if settingSwitch(c, &a.autoUpdate, a.tr("自动检查更新"), a.tr("每 24 小时在后台检查一次，有新版本时提示。")) {
		updater.SetAutomaticChecks(a.autoUpdate)
	}
}

// updateStateText explains what the update row shows: why this build cannot
// update itself, when it last looked, or that it never has.
func (a *app) updateStateText() string {
	switch {
	case !a.updatesEnabled:
		return "这个版本不能自动更新：开发构建，或安装位置不可写（例如直接从磁盘映像运行）。"
	case a.lastCheck.IsZero():
		return "还没有检查过更新。"
	default:
		return "上次检查：" + a.lastCheck.Local().Format("2006-01-02 15:04")
	}
}
