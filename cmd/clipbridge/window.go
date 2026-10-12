package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/clipbridge/internal/clip"
	"github.com/2fa0/clipbridge/internal/history"
	"github.com/2fa0/clipbridge/internal/identity"
	clipsync "github.com/2fa0/clipbridge/internal/sync"
)

const (
	pageSettings = "settings"
	pageDevices  = "devices"
	pageHistory  = "history"
)

type app struct {
	engine *clipsync.Engine
	hist   *history.Store
	board  clip.Board
	root   context.Context

	deviceID    string
	fingerprint string
	name        string
	listen      string
	addrs       []string

	page    string
	peer    string
	status  string
	flash   string
	confirm bool

	devices  []clipsync.Device
	entries  []history.Entry
	histSel  int
	histList ui.ListState
	thumbs   map[string]*ui.Bitmap

	win        *mygo.Window
	dialCancel context.CancelFunc
}

func newApp(engine *clipsync.Engine, hist *history.Store, id *identity.Identity, listen string, addrs []string, peer string) *app {
	a := &app{
		engine:      engine,
		hist:        hist,
		board:       engine.Board,
		deviceID:    id.DeviceID,
		fingerprint: identity.FormatFingerprint(id.Fingerprint),
		name:        engine.Name,
		listen:      listen,
		addrs:       addrs,
		page:        pageSettings,
		peer:        peer,
		status:      "未连接",
		histSel:     -1,
		thumbs:      map[string]*ui.Bitmap{},
		entries:     hist.List(),
		devices:     engine.Peers.List(),
	}
	a.histList.Key = func(i int) any {
		if i < 0 || i >= len(a.entries) {
			return i
		}
		return a.entries[i].ID
	}
	a.histList.Label = func(i int) string {
		if i < 0 || i >= len(a.entries) {
			return ""
		}
		return entryTitle(a.entries[i])
	}
	a.histList.Selected = &a.histSel
	return a
}

func (a *app) open(ctx context.Context, peerAddr string) {
	a.root = ctx
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:     "ClipBridge",
		Width:     880,
		Height:    560,
		MinWidth:  720,
		MinHeight: 440,
		Content:   ui.View(a.view),
	})
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "设置", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.show(pageSettings) }},
		{Label: "连接设备", Accelerator: "CmdOrCtrl+2", Click: func(*mygo.MenuItem, *mygo.Window) { a.show(pageDevices) }},
		{Label: "复制历史", Accelerator: "CmdOrCtrl+3", Click: func(*mygo.MenuItem, *mygo.Window) { a.show(pageHistory) }},
		{Role: mygo.RoleEditMenu},
	}))
	if peerAddr != "" {
		a.peer = peerAddr
		a.connect()
	}
}

func (a *app) show(page string) {
	if a.win == nil {
		a.page = page
		return
	}
	a.win.Update(func() { a.page = page })
}

func (a *app) onNotice(n clipsync.Notice) {
	var added *history.Entry
	if (n.Kind == clipsync.NoticeLocal || n.Kind == clipsync.NoticeRemote) && n.Item.Kind != "" {
		dir := history.DirLocal
		if n.Kind == clipsync.NoticeRemote {
			dir = history.DirRemote
		}
		entry, err := a.hist.Add(dir, n.PeerName, n.Item)
		if err != nil {
			n.Kind = clipsync.NoticeError
			n.Message = "写入历史失败"
		} else {
			added = &entry
		}
	}
	apply := func() { a.apply(n, added) }
	if a.win == nil {
		apply()
		return
	}
	a.win.Update(apply)
}

func (a *app) apply(n clipsync.Notice, added *history.Entry) {
	switch n.Kind {
	case clipsync.NoticeStatus, clipsync.NoticeError:
		a.status = n.Message
		a.devices = a.engine.Peers.List()
	case clipsync.NoticeLocal, clipsync.NoticeRemote:
		if n.Message != "" {
			a.flash = n.Message
		}
		if added != nil {
			a.entries = a.hist.List()
		}
	}
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(t.Background).Children(func() {
		ui.Column(c).Width(168).FillHeight().Background(t.Surface).Children(func() {
			ui.Text(c, "ClipBridge").Bold().Padding(16, 14, 8, 14)
			if ui.Sidebar(c, &a.page, func() {
				ui.SidebarItem(c, pageSettings, nil, "设置")
				ui.SidebarItem(c, pageDevices, nil, "连接设备")
				ui.SidebarItem(c, pageHistory, nil, "复制历史")
			}).Grow(1).Changed() {
				a.confirm = false
			}
			ui.Text(c, a.status).FontSize(12).TextColor(t.TextMuted).Padding(8, 14, 14, 14).MaxLines(3)
		})
		ui.Column(c).Grow(1).FillHeight().Padding(20, 24).Gap(14).Children(func() {
			switch a.page {
			case pageDevices:
				a.devicesPage(c)
			case pageHistory:
				a.historyPage(c)
			default:
				a.settingsPage(c)
			}
		})
	})
}

func (a *app) settingsPage(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "设置").FontSize(22).Bold()
	ui.Text(c, "配对在这里。开机启动、快捷键和同步范围以后也加在这一页。").TextColor(t.TextMuted)

	section(c, "本机")
	kv(c, "名称", a.name)
	kv(c, "设备", a.deviceID)
	ui.Text(c, "指纹").FontSize(12).TextColor(t.TextMuted)
	ui.Text(c, a.fingerprint).Selectable().FontFeatures("tnum")
	if len(a.addrs) == 0 {
		kv(c, "地址", "没有可用的局域网 IPv4")
	} else {
		for _, addr := range a.addrs {
			kv(c, "地址", addr)
		}
	}

	section(c, "配对")
	ui.Text(c, "填写另一台电脑的地址。只写 IP 时端口用 47821。第一次连上会信任对方证书，请和对端这一页的指纹核对。").TextColor(t.TextMuted)
	var connect bool
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.TextInput(c.Key("peer"), &a.peer).Placeholder("192.168.1.20").Grow(1).Submitted() {
			connect = true
		}
		if ui.PrimaryButton(c, "连接").Clicked() {
			connect = true
		}
	})
	if connect {
		a.connect()
	}

	section(c, "信任")
	label := "清除全部信任"
	if a.confirm {
		label = "再点一次，确认清除"
	}
	if ui.Button(c, label).Clicked() {
		if !a.confirm {
			a.confirm = true
			return
		}
		a.confirm = false
		if err := a.engine.Peers.Reset(); err != nil {
			a.status = "清除信任失败"
			return
		}
		a.devices = nil
		a.status = "已清除信任。下次连接会重新核对指纹"
	}
}

func (a *app) devicesPage(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "连接设备").FontSize(22).Bold()
	link, up := a.engine.Link()
	if up {
		ui.Textf(c, "当前在线：%s", displayName(link.Name, link.ID)).TextColor(t.TextMuted)
	} else {
		ui.Text(c, "当前没有在线设备。已信任的设备断线后仍留在这里。").TextColor(t.TextMuted)
	}
	if len(a.devices) == 0 {
		ui.Text(c, "还没有配对过的设备。到「设置」里填对端地址。").Padding(8, 0)
		return
	}
	var forgetID string
	ui.List(c, nil, len(a.devices), func(i int) {
		dev := a.devices[i]
		online := up && dev.ID == link.ID
		ui.Row(c).Padding(10, 0).Gap(12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				title := displayName(dev.Peer.Name, dev.ID)
				if online {
					title += "  ·  在线"
				}
				ui.Text(c, title).SingleLine()
				meta := platformLabel(dev.Peer.Platform)
				if dev.Peer.Addr != "" {
					meta += "  ·  " + dev.Peer.Addr
				}
				ui.Text(c, meta).FontSize(12).TextColor(t.TextMuted).SingleLine()
				ui.Text(c, identity.FormatFingerprint(dev.Peer.Fingerprint)).FontSize(12).TextColor(t.TextMuted).Selectable().SingleLine()
			})
			if ui.Button(c.Key(dev.ID), "移除").Clicked() {
				forgetID = dev.ID
			}
		})
	}).Grow(1)
	if forgetID != "" {
		a.forget(forgetID)
	}
}

func (a *app) historyPage(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "复制历史").FontSize(22).Bold()
			ui.Text(c, "本机复制和对端同步过来的文字、图片。双击一条，或选中后写回剪贴板。").TextColor(t.TextMuted)
		})
		if ui.PrimaryButton(c, "写回剪贴板").Disabled(a.histSel < 0 || a.histSel >= len(a.entries)).Clicked() {
			a.pasteHistory()
		}
	})
	if a.flash != "" {
		ui.Text(c, a.flash).FontSize(12).TextColor(t.TextMuted)
	}
	if len(a.entries) == 0 {
		ui.Text(c, "还没有记录。在这台电脑上复制一段文字，或等对端同步过来。").Padding(8, 0)
		return
	}
	if ui.List(c, &a.histList, len(a.entries), func(i int) {
		entry := a.entries[i]
		ui.Row(c).Padding(8, 4).Gap(10).AlignItems(ui.Center).Children(func() {
			if entry.Kind == string(clip.KindImage) {
				if bmp := a.thumb(entry.ID); bmp != nil {
					ui.Image(c, bmp).Size(40, 40).Fit(ui.Cover).Radius(4)
				} else {
					ui.Box(c).Size(40, 40).Radius(4).Background(t.Surface)
				}
			}
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, entryTitle(entry)).SingleLine()
				ui.Text(c, entryMeta(entry)).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
		})
	}).Grow(1).Submitted() {
		a.pasteHistory()
	}
}

func (a *app) connect() {
	addr, err := normalizePeer(a.peer)
	if err != nil || addr == "" {
		a.status = "填写对端 IP，例如 192.168.1.20"
		return
	}
	a.peer = addr
	if a.dialCancel != nil {
		a.dialCancel()
	}
	parent := a.root
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	a.dialCancel = cancel
	a.status = "正在连接 " + addr
	go clipsync.Dial(ctx, addr, a.engine)
}

func (a *app) forget(id string) {
	if err := a.engine.Peers.Forget(id); err != nil {
		a.status = "移除设备失败"
		return
	}
	a.devices = a.engine.Peers.List()
	a.status = "已移除设备。若它还在线，断开后不会再自动信任"
}

func (a *app) pasteHistory() {
	if a.histSel < 0 || a.histSel >= len(a.entries) {
		return
	}
	entry := a.entries[a.histSel]
	item, err := a.hist.Item(entry.ID)
	if err != nil {
		a.flash = "读历史失败"
		return
	}
	if err := a.board.Write(item); err != nil {
		a.flash = "写回剪贴板失败"
		return
	}
	a.flash = "已写回剪贴板"
}

func (a *app) thumb(id string) *ui.Bitmap {
	if bmp, ok := a.thumbs[id]; ok {
		return bmp
	}
	raw, err := a.hist.Thumb(id)
	if err != nil || len(raw) == 0 {
		a.thumbs[id] = nil
		return nil
	}
	bmp, err := ui.DecodeBitmap(raw)
	if err != nil {
		a.thumbs[id] = nil
		return nil
	}
	a.thumbs[id] = bmp
	return bmp
}

func section(c *ui.Context, title string) {
	ui.Text(c, title).Bold().Padding(6, 0, 0, 0)
}

func kv(c *ui.Context, k, v string) {
	t := c.Theme()
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, k).Width(48).TextColor(t.TextMuted)
		ui.Text(c, v).Selectable().SingleLine()
	})
}

func entryTitle(e history.Entry) string {
	if e.Kind == string(clip.KindImage) {
		if e.Width > 0 {
			return fmt.Sprintf("图片  %d×%d", e.Width, e.Height)
		}
		return "图片"
	}
	text := strings.TrimSpace(e.Text)
	text = strings.ReplaceAll(text, "\n", " ")
	if text == "" {
		return "空文本"
	}
	runes := []rune(text)
	if len(runes) > 80 {
		return string(runes[:80]) + "…"
	}
	return text
}

func entryMeta(e history.Entry) string {
	from := "本机"
	if e.Dir == history.DirRemote {
		from = "对端"
		if e.Peer != "" {
			from = e.Peer
		}
	}
	return e.At.Format("01-02 15:04") + "  ·  " + from + "  ·  " + formatSize(e.Size)
}

func formatSize(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

func displayName(name, id string) string {
	if name != "" {
		return name
	}
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "未知设备"
	}
	return id
}

func platformLabel(p string) string {
	switch p {
	case "macos":
		return "macOS"
	case "windows":
		return "Windows"
	case "":
		return "未知系统"
	default:
		return p
	}
}

func lanAddrs(listen string) []string {
	port := portOf(listen)
	var out []string
	for _, addr := range lanIPv4() {
		out = append(out, addr.name+"  "+addr.ip+":"+port)
	}
	return out
}
