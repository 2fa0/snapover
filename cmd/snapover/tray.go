package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/2fa0/snapover/resources"
)

func (a *app) startTray() {
	template := runtime.GOOS == "darwin"
	icon, err := a.menuIcon(template)
	if err != nil && a.engine.Log != nil {
		a.engine.Log.Warn("托盘图标没做成", "err", err)
	}
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:           icon,
		IconIsTemplate: template,
		ToolTip:        "Snapover",
	})
	if err != nil {
		if a.engine.Log != nil {
			a.engine.Log.Warn("托盘没起来", "err", err)
		}
		return
	}
	a.tray = tray
	if runtime.GOOS == "linux" {
		a.refreshTray()
		return
	}
	a.tray.OnClick(a.handleTrayClick)
	a.tray.OnRightClick(func() { a.togglePicker() })
	if !template {
		mygo.Theme.OnUpdated(func() {
			icon, err := a.menuIcon(false)
			if err != nil || a.tray == nil {
				return
			}
			_ = a.tray.SetIcon(icon, false)
		})
	}
	a.refreshTray()
}

func (a *app) menuIcon(template bool) ([]byte, error) {
	return trayIconPNG(a.loadMenuIcon(), !template && mygo.Theme.IsDark())
}

func (a *app) menuBarIconSettings(c *ui.Context) {
	selected := a.cfg.Values.MenuBarIcon
	if ui.Segmented(c, &selected, "原图标", "icon1", "icon2", "icon3").Label("菜单栏图标样式").Changed() {
		a.saveMenuBarIcon(selected)
	}
	ui.Text(c, "选择后立即生效，并随系统适配深浅色。").FontSize(12).TextColor(c.Theme().TextMuted)
	if a.menuBarIconError != "" {
		ui.Text(c, a.menuBarIconError).FontSize(12).TextColor(c.Theme().Danger)
	}
}

func (a *app) saveMenuBarIcon(selected int) {
	previous := a.cfg.Values.MenuBarIcon
	a.cfg.Values.MenuBarIcon = selected
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values.MenuBarIcon = previous
		a.menuBarIconError = "保存图标失败，选择未生效，请检查数据目录权限后重试"
		return
	}
	a.menuBarIconError = ""
	if a.tray != nil {
		icon, err := a.menuIcon(true)
		if err == nil {
			err = a.tray.SetIcon(icon, true)
		}
		if err != nil {
			a.menuBarIconError = "图标已保存，但切换失败，请重启 Snapover 后重试"
		}
	}
}

func (a *app) handleTrayClick() {
	if runtime.GOOS == "darwin" {
		if a.trayMenuOpen {
			return
		}
		if a.picker != nil {
			a.closePicker()
			return
		}
		// performClick from inside the status item's left-up action does
		// not start menu tracking, so the menu stays until another click
		// on the icon. Run it on the next turn of the main loop.
		a.trayMenuOpen = true
		go func() {
			mygo.RunOnMain(func() {
				defer func() { a.trayMenuOpen = false }()
				a.popupTrayMenu()
			})
		}()
		return
	}
	a.closePicker()
	a.popupTrayMenu()
}

func (a *app) popupTrayMenu() {
	if a.tray == nil {
		return
	}
	a.tray.SetMenu(a.trayMenu())
	defer a.tray.SetMenu(nil)
	a.tray.PopUpMenu()
}

func (a *app) refreshTray() {
	if a.tray == nil {
		return
	}
	status := a.displayStatus()
	if a.paused {
		status = "已暂停"
	}
	if a.engine.IgnoreNextCopyPending() {
		status = "等待忽略下一次复制（不记录、不同步）"
	}
	if status == "" {
		status = "Snapover"
	}
	a.tray.SetToolTip(status)
	if runtime.GOOS == "linux" {
		a.tray.SetMenu(a.trayMenu())
	}
}

func (a *app) trayMenu() *mygo.Menu {
	status := a.displayStatus()
	if a.paused {
		status = "已暂停"
	}
	if status == "" {
		status = "Snapover"
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Snapover · " + appVersionLabel(), Disabled: true},
		{Label: status, Disabled: true},
		{Label: "快速历史", Click: func(*mygo.MenuItem, *mygo.Window) { a.togglePicker() }},
		{Label: "打开窗口", Click: func(*mygo.MenuItem, *mygo.Window) { a.reveal(a.page) }},
		{Label: "连接设备", Click: func(*mygo.MenuItem, *mygo.Window) { a.reveal(pageDevices) }},
		{Label: "设置", Click: func(*mygo.MenuItem, *mygo.Window) { a.reveal(pageSettings) }},
		{Label: "检查更新…", Click: func(*mygo.MenuItem, *mygo.Window) { a.checkForUpdates() }},
		mygo.Separator(),
		{
			ID:     "cancel-ignore-next-copy",
			Label:  "取消忽略下一次复制",
			Hidden: !a.engine.IgnoreNextCopyPending(),
			Click:  func(*mygo.MenuItem, *mygo.Window) { a.setIgnoreNextCopy(false) },
		},
		{
			Label:   "暂停同步",
			Type:    mygo.MenuItemCheckbox,
			Checked: a.paused,
			Click: func(item *mygo.MenuItem, _ *mygo.Window) {
				a.paused = item.Checked
				a.saveSettings()
			},
		},
		{
			Label:   "开机启动",
			Type:    mygo.MenuItemCheckbox,
			Checked: a.openAtLogin,
			Click: func(item *mygo.MenuItem, _ *mygo.Window) {
				a.setOpenAtLogin(item.Checked)
			},
		},
		mygo.Separator(),
		{Label: "退出", Role: mygo.RoleQuit},
	})
}

func (a *app) ignoreNextCopySettings(c *ui.Context) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "忽略下一次复制").Bold().Grow(1).Tooltip("该次复制不记录历史，也不同步")
		if a.engine.IgnoreNextCopyPending() {
			ui.Text(c, "等待复制…").FontSize(12).TextColor(c.Theme().TextMuted)
			if ui.Button(c, "取消").Label("取消忽略下一次复制").Clicked() {
				a.setIgnoreNextCopy(false)
			}
		} else if ui.Button(c, "忽略一次").Label("启用忽略下一次复制").Tooltip("该次复制不记录历史，也不同步").Clicked() {
			a.setIgnoreNextCopy(true)
		}
	})
}

func (a *app) setIgnoreNextCopy(on bool) {
	if err := a.engine.SetIgnoreNextCopy(on); err != nil {
		a.status = "无法忽略下一次复制，请稍后重试：" + err.Error()
	} else if on {
		a.status = "等待下一次复制，该次内容不记录、不同步"
	} else {
		a.status = "已取消忽略下一次复制"
	}
	a.refreshTray()
	if a.win != nil {
		a.win.Invalidate()
	}
}

func (a *app) reveal(page string) {
	if page == "" {
		page = pageSettings
	}
	a.page = page
	a.openMainWindow()
	if a.win == nil {
		return
	}
	a.win.Show()
	a.win.Focus()
	a.win.Invalidate()
}

func (a *app) setOpenAtLogin(on bool) {
	if err := mygo.App.SetOpenAtLogin(on); err != nil {
		a.openAtLogin = mygo.App.OpenAtLogin()
		a.status = loginErr(err)
	} else {
		a.openAtLogin = on
		if on {
			a.status = "已设置开机启动"
		} else {
			a.status = "已关闭开机启动"
		}
	}
	if a.cfg != nil {
		a.cfg.Values.OpenAtLogin = a.openAtLogin
		if err := a.cfg.Save(); err != nil && a.engine.Log != nil {
			a.engine.Log.Warn("保存开机启动设置失败", "err", err)
		}
	}
	a.refreshTray()
}

func (a *app) restoreLogin() {
	a.openAtLogin = mygo.App.OpenAtLogin()
	if a.cfg == nil || !a.cfg.Values.OpenAtLogin || a.openAtLogin {
		return
	}
	if err := mygo.App.SetOpenAtLogin(true); err != nil {
		a.status = loginErr(err)
		a.cfg.Values.OpenAtLogin = false
		_ = a.cfg.Save()
		return
	}
	a.openAtLogin = true
}

func loginErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "bundle"):
		return "开机启动需要用打好的 Snapover.app，源码直接运行不行"
	case strings.Contains(s, "Login Items"), strings.Contains(s, "allow"):
		return "到系统设置的登录项里允许 Snapover"
	default:
		return "开机启动没改成：" + s
	}
}

func (a *app) loadMenuIcon() []byte {
	if runtime.GOOS == "darwin" && a.cfg != nil {
		switch a.cfg.Values.MenuBarIcon {
		case 1:
			return resources.Icon1PNG
		case 2:
			return resources.Icon2PNG
		case 3:
			return resources.Icon3PNG
		}
	}
	// 嵌入程序图标，Windows 上单独拷走 exe 也能显示托盘图标。
	return resources.IconPNG
}

func trayIconPNG(src []byte, white bool) ([]byte, error) {
	const size = 32
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	if len(src) == 0 {
		fillFallback(dst, white)
	} else {
		img, _, err := image.Decode(bytes.NewReader(src))
		if err != nil {
			return nil, err
		}
		cropped := cropAlpha(img)
		inner := image.Rect(3, 3, size-3, size-3)
		width, height := cropped.Bounds().Dx(), cropped.Bounds().Dy()
		if width > height {
			height = max(1, height*inner.Dx()/width)
			inner.Min.Y += (inner.Dy() - height) / 2
			inner.Max.Y = inner.Min.Y + height
		} else {
			width = max(1, width*inner.Dy()/height)
			inner.Min.X += (inner.Dx() - width) / 2
			inner.Max.X = inner.Min.X + width
		}
		scaled := image.NewNRGBA(image.Rect(0, 0, inner.Dx(), inner.Dy()))
		scaleBox(cropped, scaled)
		ink := color.NRGBA{A: 255}
		if white {
			ink = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
		for y := 0; y < scaled.Bounds().Dy(); y++ {
			for x := 0; x < scaled.Bounds().Dx(); x++ {
				r, g, b, a := scaled.At(x, y).RGBA()
				// RGBA 已预乘透明度，同时保留原透明区域并将白色细节镂空。
				alpha := a - (r+g+b)/3
				dst.SetNRGBA(inner.Min.X+x, inner.Min.Y+y, color.NRGBA{R: ink.R, G: ink.G, B: ink.B, A: uint8(alpha >> 8)})
			}
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func fillFallback(dst *image.NRGBA, white bool) {
	ink := color.NRGBA{A: 255}
	if white {
		ink = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	}
	for y := 6; y < 26; y++ {
		for x := 10; x < 22; x++ {
			dst.SetNRGBA(x, y, ink)
		}
	}
}

func cropAlpha(img image.Image) image.Image {
	b := img.Bounds()
	minx, miny, maxx, maxy := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			if x < minx {
				minx = x
			}
			if y < miny {
				miny = y
			}
			if x > maxx {
				maxx = x
			}
			if y > maxy {
				maxy = y
			}
		}
	}
	if maxx < minx || maxy < miny {
		return img
	}
	sub, ok := img.(interface {
		SubImage(r image.Rectangle) image.Image
	})
	if !ok {
		return img
	}
	return sub.SubImage(image.Rect(minx, miny, maxx+1, maxy+1))
}

func scaleBox(src image.Image, dst *image.NRGBA) {
	sb := src.Bounds()
	db := dst.Bounds()
	dw, dh := db.Dx(), db.Dy()
	for y := 0; y < dh; y++ {
		y0 := sb.Min.Y + y*sb.Dy()/dh
		y1 := sb.Min.Y + (y+1)*sb.Dy()/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0 := sb.Min.X + x*sb.Dx()/dw
			x1 := sb.Min.X + (x+1)*sb.Dx()/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var rs, gs, bs, as, n uint32
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					r, g, b, a := src.At(xx, yy).RGBA()
					rs += r
					gs += g
					bs += b
					as += a
					n++
				}
			}
			dst.SetNRGBA(db.Min.X+x, db.Min.Y+y, color.NRGBAModel.Convert(color.RGBA64{
				R: uint16(rs / n),
				G: uint16(gs / n),
				B: uint16(bs / n),
				A: uint16(as / n),
			}).(color.NRGBA))
		}
	}
}
