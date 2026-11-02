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

	"github.com/2fa0/snapover/internal/resources"
	"github.com/2fa0/snapover/internal/settings"
)

func (a *app) startTray() {
	// Windows templates follow the taskbar's theme, which can differ from apps.
	template := runtime.GOOS == "darwin" || runtime.GOOS == "windows"
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
	white := !template && mygo.Theme.IsDark()
	selected := settings.DefaultMenuBarIcon
	if a.cfg != nil {
		selected = a.cfg.Values.MenuBarIcon
	}
	return menuBarIconPNG(selected, white)
}

func (a *app) menuBarIconSettings(c *ui.Context) {
	t := c.Theme()
	ids := [...]int{2, 1, 3}
	selected := 0
	for i, id := range ids {
		if id == a.cfg.Values.MenuBarIcon {
			selected = i
		}
	}
	parts := ui.SegmentedBase(c, &selected, len(ids))
	parts.Track.Label(a.tr("图标样式")).Gap(12).FillWidth().MaxWidth(360).Children(func() {
		for i, label := range []string{a.tr("邮筒"), a.tr("小鸟"), a.tr("信封")} {
			option := parts.Segment(i).Label(label).Grow(1).Basis(0).MinWidth(0).
				Padding(12, 8).Radius(8).Border(1, t.Border).Background(t.Background)
			if option.Hovered() {
				option.Background(t.SurfaceHover)
			}
			if selected == i {
				option.BorderColor(t.Accent)
			}
			option.Children(func() {
				ui.Column(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if bmp := a.menuBarIconPreview(ids[i], t.Dark); bmp != nil {
						ui.Image(c, bmp).Size(32, 32).Fit(ui.Contain)
					}
					text := ui.Text(c, label).FontSize(12)
					if selected == i {
						text.Bold().TextColor(t.Accent)
					}
				})
			})
		}
	})
	if parts.Track.Changed() {
		a.saveMenuBarIcon(ids[selected])
	}
	if a.menuBarIconError != "" {
		ui.Text(c, a.tr(a.menuBarIconError)).FontSize(12).TextColor(c.Theme().Danger)
	}
}

func (a *app) menuBarIconPreview(selected int, dark bool) *ui.Bitmap {
	if a.menuBarPreviewDark != dark {
		a.menuBarPreviews = [3]*ui.Bitmap{}
		a.menuBarPreviewDark = dark
	}
	index := selected - 1
	if a.menuBarPreviews[index] == nil {
		data, err := menuBarIconPNG(selected, dark)
		if err != nil {
			return nil
		}
		bmp, err := ui.DecodeBitmap(data)
		if err != nil {
			return nil
		}
		a.menuBarPreviews[index] = bmp
	}
	return a.menuBarPreviews[index]
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
		template := runtime.GOOS == "darwin" || runtime.GOOS == "windows"
		icon, err := a.menuIcon(template)
		if err == nil {
			err = a.tray.SetIcon(icon, template)
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
	a.tray.SetToolTip(a.tr(status))
	if runtime.GOOS == "linux" {
		a.tray.SetMenu(a.trayMenu())
	}
}

func (a *app) trayMenu() *mygo.Menu {
	// 菜单只放动作，不放版本号和连接状态：状态在托盘提示里，页面切换在窗口侧栏里。
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: a.tr("快速历史"), Click: func(*mygo.MenuItem, *mygo.Window) { a.togglePicker() }},
		{Label: a.tr("设置"), Click: func(*mygo.MenuItem, *mygo.Window) { a.reveal(pageSettings) }},
		{Label: a.tr("检查更新…"), Click: func(*mygo.MenuItem, *mygo.Window) { a.checkForUpdates() }},
		mygo.Separator(),
		{
			ID:     "cancel-ignore-next-copy",
			Label:  a.tr("取消忽略下一次复制"),
			Hidden: !a.engine.IgnoreNextCopyPending(),
			Click:  func(*mygo.MenuItem, *mygo.Window) { a.setIgnoreNextCopy(false) },
		},
		{
			Label:   a.tr("暂停同步"),
			Type:    mygo.MenuItemCheckbox,
			Checked: a.paused,
			Click: func(item *mygo.MenuItem, _ *mygo.Window) {
				a.paused = item.Checked
				a.saveSettings()
			},
		},
		{
			Label:   a.tr("开机启动"),
			Type:    mygo.MenuItemCheckbox,
			Checked: a.openAtLogin,
			Click: func(item *mygo.MenuItem, _ *mygo.Window) {
				a.setOpenAtLogin(item.Checked)
			},
		},
		mygo.Separator(),
		{Label: a.tr("退出"), Role: mygo.RoleQuit},
	})
}

func (a *app) ignoreNextCopySettings(c *ui.Context) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.tr("忽略下一次复制")).Bold().Grow(1).Tooltip(a.tr("该次复制不记录历史，也不同步"))
		if a.engine.IgnoreNextCopyPending() {
			ui.Text(c, a.tr("等待复制…")).FontSize(12).TextColor(c.Theme().TextMuted)
			if ui.Button(c, a.tr("取消")).Label(a.tr("取消忽略下一次复制")).Clicked() {
				a.setIgnoreNextCopy(false)
			}
		} else if ui.Button(c, a.tr("忽略一次")).Label(a.tr("启用忽略下一次复制")).Tooltip(a.tr("该次复制不记录历史，也不同步")).Clicked() {
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

func menuIconSource(selected int) []byte {
	switch selected {
	case 1:
		return resources.Icon1PNG
	case 3:
		return resources.Icon3PNG
	default:
		return resources.Icon2PNG
	}
}

func menuBarIconPNG(selected int, white bool) ([]byte, error) {
	if selected < 1 || selected > 3 {
		selected = settings.DefaultMenuBarIcon
	}
	img, _, err := image.Decode(bytes.NewReader(menuIconSource(selected)))
	if err != nil {
		return nil, err
	}
	// Crop the visible template, not white details or faint export remnants
	// that would disappear later but still shift or shrink the actual icon.
	mask := image.NewNRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			mask.SetNRGBA(x, y, color.NRGBA{A: uint8((a - (r+g+b)/3) >> 8)})
		}
	}
	inner := image.Rect(3, 3, 29, 29)
	switch selected {
	case 1:
		// The bird's raised wing needs a slight optical shift downward.
		inner = inner.Add(image.Pt(0, 1))
	case 2:
		// The narrow mailbox needs more height for comparable visual weight.
		inner = image.Rect(1, 1, 31, 31)
	case 3:
		// Balance the envelope's solid top flap after removing its export margins.
		inner = inner.Add(image.Pt(0, 1))
	}
	return renderTrayIconPNG(cropAlphaAbove(mask, 0x1000), white, inner)
}

func renderTrayIconPNG(img image.Image, white bool, inner image.Rectangle) ([]byte, error) {
	const size = 32
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	if img == nil {
		fillFallback(dst, white)
	} else {
		cropped := cropAlpha(img)
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
	return cropAlphaAbove(img, 0)
}

func cropAlphaAbove(img image.Image, threshold uint32) image.Image {
	b := img.Bounds()
	minx, miny, maxx, maxy := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a <= threshold {
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
