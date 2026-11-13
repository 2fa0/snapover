package main

import (
	"runtime"

	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func appView(view func(*ui.Context)) *ui.Content {
	return ui.View(func(c *ui.Context) {
		if runtime.GOOS == "windows" {
			theme := *c.Theme()
			// Keep Chinese glyphs in one UI family instead of locale-dependent fallback.
			theme.Font = "Microsoft YaHei UI, Microsoft YaHei, Segoe UI, sans-serif"
			c.SetTheme(&theme)
		}
		view(c)
	})
}

func configureTheme(theme string) {
	source := mygo.ThemeSystem
	switch theme {
	case settings.ThemeLight:
		source = mygo.ThemeLight
	case settings.ThemeDark:
		source = mygo.ThemeDark
	}
	mygo.Theme.SetSource(source)
}

func (a *app) themeSettings(c *ui.Context) {
	selected := 0
	if a.cfg != nil {
		switch a.cfg.Values.Theme {
		case settings.ThemeLight:
			selected = 1
		case settings.ThemeDark:
			selected = 2
		}
	}
	settingRow(c, a.tr("界面主题"), "", func() {
		if ui.Segmented(c, &selected, a.tr("跟随系统"), a.tr("浅色"), a.tr("深色")).Label(a.tr("界面主题")).Changed() {
			a.saveThemeChoice(selected)
		}
	})
	if a.themeError != "" {
		ui.Text(c, a.tr(a.themeError)).FontSize(12).TextColor(c.Theme().Danger)
	}
}

func (a *app) saveThemeChoice(selected int) {
	if a.cfg == nil {
		return
	}
	want := settings.ThemeAuto
	switch selected {
	case 1:
		want = settings.ThemeLight
	case 2:
		want = settings.ThemeDark
	}
	prev := a.cfg.Values.Theme
	if prev == want {
		a.themeError = ""
		return
	}
	a.cfg.Values.Theme = want
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values.Theme = prev
		a.themeError = "保存设置失败：" + err.Error()
		return
	}
	a.themeError = ""
	configureTheme(want)
}
