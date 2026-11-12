package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *app) savePrivacy() {
	previousMode, previousApps := a.cfg.Values.PrivacyMode, a.cfg.Values.ExcludedApps
	apps := strings.FieldsFunc(a.excludedAppsText, func(r rune) bool { return r == ';' || r == '；' || r == '\n' })
	cleaned := make([]string, 0, len(apps))
	for _, app := range apps {
		if name := strings.TrimSpace(app); name != "" {
			cleaned = append(cleaned, name)
		}
	}
	a.cfg.Values.PrivacyMode, a.cfg.Values.ExcludedApps = a.privacyMode, cleaned
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values.PrivacyMode, a.cfg.Values.ExcludedApps = previousMode, previousApps
		a.privacyMode = previousMode
		a.privacyError = "隐私设置保存失败，请检查数据目录权限后重试"
		return
	}
	a.engine.SetPrivacyPolicy(a.privacyMode, cleaned)
	a.excludedAppsText = strings.Join(cleaned, "; ")
	a.privacyError = ""
	a.refreshTray()
}

func (a *app) privacySettings(c *ui.Context) {
	if settingSwitch(c, &a.privacyMode, a.tr("隐私模式"), "") {
		a.savePrivacy()
	}
	ui.Collapsible(c, a.tr("排除来源应用"), &a.showPrivacy, func() {
		ui.Column(c).Gap(8).Padding(12, 0).Children(func() {
			ui.TextInput(c.Key("excluded-apps"), &a.excludedAppsText).Label(a.tr("排除的应用名称")).Placeholder(a.tr("Safari; 1Password，分号分隔")).Grow(1)
			if ui.Button(c, a.tr("保存应用排除")).Clicked() {
				a.savePrivacy()
			}
		})
	})
	if a.privacyError != "" {
		errorMessage(c, a.tr(a.privacyError))
	}
}
