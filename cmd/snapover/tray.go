package main

import (
	"bytes"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
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
	a.tray.OnClick(func() { a.togglePicker() })
	a.tray.OnRightClick(func() {
		a.closePicker()
		a.tray.SetMenu(a.trayMenu())
		defer a.tray.SetMenu(nil)
		a.tray.PopUpMenu()
	})
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
	return trayIconPNG(loadMenuIcon(), !template && mygo.Theme.IsDark())
}

func (a *app) refreshTray() {
	if a.tray == nil {
		return
	}
	status := a.status
	if a.paused {
		status = "已暂停"
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
	status := a.status
	if a.paused {
		status = "已暂停"
	}
	if status == "" {
		status = "Snapover"
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: status, Disabled: true},
		{Label: "快速历史", Click: func(*mygo.MenuItem, *mygo.Window) { a.togglePicker() }},
		{Label: "打开窗口", Click: func(*mygo.MenuItem, *mygo.Window) { a.reveal(a.page) }},
		{Label: "设置", Click: func(*mygo.MenuItem, *mygo.Window) { a.reveal(pageSettings) }},
		mygo.Separator(),
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

func (a *app) reveal(page string) {
	if page == "" {
		page = pageSettings
	}
	a.page = page
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

func loadMenuIcon() []byte {
	// icon.jpg 是菜单栏图标。程序图标是 icon.png，由 app.jpg 转来，不要拿来做菜单。
	names := []string{"icon.jpg"}
	if dir, err := mygo.App.Path(mygo.PathResources); err == nil {
		for _, name := range names {
			if buf, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				return buf
			}
		}
	}
	for _, name := range names {
		if buf, err := os.ReadFile(filepath.Join("resources", name)); err == nil {
			return buf
		}
	}
	return nil
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
		cropped := cropInk(img)
		inner := image.Rect(3, 3, size-3, size-3)
		scaled := image.NewNRGBA(image.Rect(0, 0, inner.Dx(), inner.Dy()))
		scaleBox(cropped, scaled)
		ink := color.NRGBA{A: 255}
		if white {
			ink = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
		for y := 0; y < scaled.Bounds().Dy(); y++ {
			for x := 0; x < scaled.Bounds().Dx(); x++ {
				r, g, b, a := scaled.At(x, y).RGBA()
				nr, ng, nb, na := uint8(r>>8), uint8(g>>8), uint8(b>>8), uint8(a>>8)
				if na < 16 || (nr > 242 && ng > 242 && nb > 242) {
					continue
				}
				lum := (int(nr) + int(ng) + int(nb)) / 3
				alpha := 255 - lum
				if alpha < 24 {
					continue
				}
				dst.SetNRGBA(inner.Min.X+x, inner.Min.Y+y, color.NRGBA{R: ink.R, G: ink.G, B: ink.B, A: uint8(alpha)})
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

func cropInk(img image.Image) image.Image {
	b := img.Bounds()
	minx, miny, maxx, maxy := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a>>8 < 16 || (r>>8 > 242 && g>>8 > 242 && bl>>8 > 242) {
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
			dst.SetNRGBA(db.Min.X+x, db.Min.Y+y, color.NRGBA{
				R: uint8((rs / n) >> 8),
				G: uint8((gs / n) >> 8),
				B: uint8((bs / n) >> 8),
				A: uint8((as / n) >> 8),
			})
		}
	}
}
