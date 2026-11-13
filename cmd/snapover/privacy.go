package main

import (
	"bytes"
	"image/png"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/2fa0/snapover/internal/apps"
	"github.com/2fa0/snapover/internal/settings"
	"github.com/2fa0/snapover/internal/sync"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// runningAppsRefresh is how long a list of running apps is reused: opening the
// section again keeps the icons it converted, and an app started meanwhile
// shows up on the next look without paying for the survey twice.
const runningAppsRefresh = 5 * time.Second

func (a *app) savePrivacy() {
	previousMode, previousWhitelist := a.cfg.Values.PrivacyMode, a.cfg.Values.ExcludedWhitelist
	previousApps := a.cfg.Values.ExcludedApps
	a.cfg.Values.PrivacyMode, a.cfg.Values.ExcludedWhitelist = a.privacyMode, a.excludedWhitelist
	a.cfg.Values.ExcludedApps = a.cfg.Values.ExcludedApps.Clean()
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values.PrivacyMode, a.cfg.Values.ExcludedWhitelist = previousMode, previousWhitelist
		a.cfg.Values.ExcludedApps = previousApps
		a.privacyMode = previousMode
		a.privacyError = "隐私设置保存失败，请检查数据目录权限后重试"
		return
	}
	a.applyPrivacyPolicy()
	a.privacyError = ""
	a.refreshTray()
}

// applyPrivacyPolicy hands the list to the engine, which decides what a copy
// is worth from then on.
func (a *app) applyPrivacyPolicy() {
	a.engine.SetPrivacyPolicy(a.privacyMode, privacyRules(a.cfg.Values), a.cfg.Values.ExcludedWhitelist)
}

// privacyRules turns the saved list into what the engine matches: entries
// switched off stay in the list without deciding anything.
func privacyRules(values settings.Values) []sync.AppRule {
	active := values.ExcludedApps.Active()
	rules := make([]sync.AppRule, 0, len(active))
	for _, entry := range active {
		rules = append(rules, sync.AppRule{ID: entry.ID, Name: entry.Name})
	}
	return rules
}

func (a *app) privacySettings(c *ui.Context) {
	if settingSwitch(c, &a.privacyMode, a.tr("隐私模式"), "") {
		a.savePrivacy()
	}
	if a.privacyError != "" {
		errorMessage(c, a.tr(a.privacyError))
	}
	// 排除名单独立于隐私模式生效，与其它折叠项同级，不挂在开关下面。
	ui.Divider(c)
	ui.Collapsible(c, a.tr("应用程序"), &a.showPrivacy, func() {
		a.applicationList(c)
	})
}

// applicationList is the list of applications the rules are about: which ones
// are left alone, or in whitelist mode which ones are the only ones kept.
func (a *app) applicationList(c *ui.Context) {
	t := c.Theme()
	if a.showPrivacy {
		a.refreshRunningApps()
	}
	entries := a.cfg.Values.ExcludedApps
	// A row reports what the user did with it, and the list changes once it
	// has been drawn: removing a row while drawing the rest shifts the ones
	// after it.
	remove, toggle := -1, -1
	toggledOn := false
	ui.Column(c).Padding(12, 0).Gap(10).Children(func() {
		if len(entries) == 0 {
			ui.Text(c, a.tr(a.emptyListNote())).FontSize(13).TextColor(t.TextMuted)
		}
		for i := range entries {
			removed, switched, on := a.applicationRow(c, entries[i])
			if removed {
				remove = i
			}
			if switched {
				toggle, toggledOn = i, on
			}
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).AlignSelf(ui.Stretch).Children(func() {
			a.addApplicationButton(c)
			ui.Row(c).Grow(1).Basis(0)
			whitelist := ui.Switch(c, &a.excludedWhitelist).Label(switchLabel(a.tr("白名单模式")))
			ui.Text(c, a.tr("白名单模式")).FontSize(13)
			if whitelist.Changed() {
				a.savePrivacy()
			}
			ui.Icon(c, infoIcon).FontSize(13).TextColor(t.TextMuted).Tooltip(a.tr("开启白名单模式后，只有名单内的应用会被记录和同步。"))
		})
	})
	switch {
	case remove >= 0:
		a.cfg.Values.ExcludedApps = append(entries[:remove:remove], entries[remove+1:]...)
		a.savePrivacy()
	case toggle >= 0:
		a.cfg.Values.ExcludedApps[toggle].Off = !toggledOn
		a.savePrivacy()
	}
}

// emptyListNote says what an empty list means, which differs by mode and is
// the one thing a user cannot see from the empty space.
func (a *app) emptyListNote() string {
	if a.cfg.Values.ExcludedWhitelist {
		return "名单为空，所有应用的复制都不再记录和同步"
	}
	return "还没有应用"
}

// applicationRow draws one app, and reports whether its remove button or its
// switch was used, and whether the switch was left on.
func (a *app) applicationRow(c *ui.Context, entry settings.ExcludedApp) (removed, switched, on bool) {
	t := c.Theme()
	ui.Row(c.Key("app-" + entry.Name + entry.ID)).Gap(10).AlignItems(ui.Center).AlignSelf(ui.Stretch).Children(func() {
		if bitmap := a.applicationIcon(entry.ID, entry.Name); bitmap != nil {
			ui.Image(c, bitmap).Size(20, 20).Shrink(0)
		} else {
			ui.Column(c).Size(20, 20).Shrink(0)
		}
		ui.Text(c, entry.Name).FontSize(13).Grow(1).Basis(0).MinWidth(0).SingleLine().Tooltip(entry.Name)
		if ui.ButtonBase(c).Label(a.tr("移除")).Padding(4).Radius(6).Tooltip(a.tr("从名单中移除")).Children(func() {
			ui.Icon(c, removeIcon).FontSize(12).TextColor(t.TextMuted)
		}).Clicked() {
			removed = true
		}
		enabled := !entry.Off
		if ui.Switch(c, &enabled).Label(switchLabel(entry.Name)).Changed() {
			switched, on = true, enabled
		}
	})
	return removed, switched, on
}

// addApplicationButton offers the apps that are running now, with the ones
// already listed shown as they are, and any other app from the Finder.
func (a *app) addApplicationButton(c *ui.Context) {
	add := ui.Button(c, a.tr("添加应用"))
	add.PopoverMenuButton(func(m *ui.Menu) {
		if len(a.runningApps) == 0 {
			m.Item(a.tr("没有运行中的应用程序")).Disabled(true)
		} else {
			m.Submenu(a.tr("运行中的应用程序"), func(m *ui.Menu) {
				for _, entry := range a.runningApps {
					listed := a.cfg.Values.ExcludedApps.Find(settings.ExcludedApp{ID: entry.ID, Name: entry.Name}) >= 0
					item := m.Item(entry.Name).Disabled(listed).Checked(listed)
					if icon := a.applicationMenuIcon(entry); icon != nil {
						item.Icon(icon)
					}
					if item.Chosen() {
						a.addApplication(settings.ExcludedApp{ID: entry.ID, Name: entry.Name})
					}
				}
			})
		}
		m.Separator()
		if m.Item(a.tr("选取应用程序…")).Chosen() {
			a.chooseApplication()
		}
	})
}

// addApplication puts one app in the list, unless it is there already.
func (a *app) addApplication(entry settings.ExcludedApp) {
	if a.cfg.Values.ExcludedApps.Find(entry) >= 0 {
		return
	}
	a.cfg.Values.ExcludedApps = append(a.cfg.Values.ExcludedApps, entry)
	a.savePrivacy()
}

// chooseApplication asks the system for an app bundle, which is also how a
// Windows user picks an executable.
func (a *app) chooseApplication() {
	ext := ".app"
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:  a.win,
		Title:   a.tr("选择应用程序"),
		Filters: []mygo.FileFilter{{Name: a.tr("应用程序"), Extensions: []string{strings.TrimPrefix(ext, ".")}}},
	})
	if err != nil || len(paths) == 0 {
		return
	}
	entry := settings.ExcludedApp{Name: strings.TrimSuffix(filepath.Base(paths[0]), ext)}
	if described, err := apps.Describe(paths[0]); err == nil {
		entry = settings.ExcludedApp{ID: described.ID, Name: described.Name}
		a.rememberIcon(described)
	}
	a.addApplication(entry)
}

// refreshRunningApps reloads the running apps off the UI thread; converting
// their icons reads a megapixel each, so it must not run while drawing.
func (a *app) refreshRunningApps() {
	// Without a window the list has nowhere to show, as in tests.
	if a.win == nil || a.runningAppsBusy || time.Since(a.runningAppsAt) < runningAppsRefresh {
		return
	}
	a.runningAppsBusy = true
	go func() {
		list := apps.Running()
		a.update(func() {
			a.runningAppsBusy = false
			a.runningApps = list
			a.runningAppsAt = time.Now()
			for _, entry := range list {
				a.rememberIcon(entry)
			}
		})
	}()
}

// rememberIcon keeps an icon after its app quits, so a row does not lose the
// icon it was added with.
func (a *app) rememberIcon(entry apps.App) {
	if entry.Icon == nil || entry.Name == "" {
		return
	}
	if a.appIcons == nil {
		a.appIcons = make(map[string][]byte)
	}
	a.appIcons[iconKey(entry.ID, entry.Name)] = entry.Icon
}

// iconKey tells two apps of one name apart, and falls back to the name of an
// app the platform could not identify.
func iconKey(id, name string) string {
	if id != "" {
		return id
	}
	return name
}

// applicationIcon is the icon stored for a listed app, which is the icon of
// the app the platform described when it was added or last ran.
func (a *app) applicationIcon(id, name string) *ui.Bitmap {
	if data := a.appIcons[iconKey(id, name)]; data != nil {
		return a.iconBitmap(iconKey(id, name), data)
	}
	if id != "" && a.appIcons[name] != nil {
		return a.iconBitmap(name, a.appIcons[name])
	}
	return nil
}

// applicationMenuIcon is the icon of one running app in the menu, made once
// per image so that opening the menu again costs no decode.
func (a *app) applicationMenuIcon(entry apps.App) *mygo.MenuIcon {
	if entry.Icon == nil {
		return nil
	}
	if a.menuIcons == nil {
		a.menuIcons = make(map[string]*mygo.MenuIcon)
	}
	key := entry.ID
	if key == "" {
		key = entry.Name
	}
	if icon, ok := a.menuIcons[key]; ok {
		return icon
	}
	icon, err := mygo.NewMenuIcon(entry.Icon, false)
	if err != nil {
		return nil
	}
	a.menuIcons[key] = icon
	return icon
}

// iconBitmap decodes an app's icon once for the rows that show it.
func (a *app) iconBitmap(key string, data []byte) *ui.Bitmap {
	if a.appBitmaps == nil {
		a.appBitmaps = make(map[string]*ui.Bitmap)
	}
	if bitmap, ok := a.appBitmaps[key]; ok {
		return bitmap
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	bitmap := ui.NewBitmap(img)
	a.appBitmaps[key] = bitmap
	return bitmap
}
