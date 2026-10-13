package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hashicorp/golang-lru/v2/simplelru"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/discovery"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/lan"
	"github.com/2fa0/snapover/internal/paste"
	"github.com/2fa0/snapover/internal/settings"
	clipsync "github.com/2fa0/snapover/internal/sync"
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

	cfg          *settings.File
	paused       bool
	textOnly     bool
	maxImageText string

	page         string
	peer         string
	query        string
	status       string
	flash        string
	confirm      bool
	confirmClear bool

	discovered       []discovery.Device
	discoveryMessage string
	discoveryScope   atomic.Value
	discoveryWake    chan struct{}
	segments         []segmentRow
	targetPeerID     string
	dialSeq          uint64
	deviceDetails    string
	confirmRemove    string

	devices       []clipsync.Device
	entries       []history.Entry
	histSel       int
	histList      ui.ListState
	thumbs        *simplelru.LRU[string, *ui.Bitmap]
	historySearch historySearch
	pickerSearch  historySearch

	win                *mygo.Window
	tray               *mygo.Tray
	picker             *mygo.Window
	pickPreview        pickerPreview
	pickLoading        bool
	pickOriginal       *mygo.Window
	quitting           bool
	openAtLogin        bool
	pickQuery          string
	pickSel            int
	pickList           ui.ListState
	pickFlash          string
	pickBusy           bool
	pasteTo            paste.Target
	dialCancel         context.CancelFunc
	dialAddr           string
	fileWin            *mygo.Window
	filePath           string
	fileSize           int64
	fileBusy           bool
	fileChoosing       bool
	fileDone           int64
	fileMessage        string
	receiveMessage     string
	lastReceived       string
	fileCommandHint    string
	fileWaiting        bool
	pendingFileRequest *fileRequest
	sendToBusy         bool
}

func newApp(engine *clipsync.Engine, hist *history.Store, cfg *settings.File, id *identity.Identity, listen string, addrs []string, peer string) *app {
	if cfg == nil {
		cfg = &settings.File{Values: settings.Values{MaxImageMB: settings.DefaultMaxImageMB}}
	}
	devices := engine.Peers.List()
	if peer == "" {
		for _, dev := range devices {
			if dev.Peer.Addr != "" {
				peer = dev.Peer.Addr
				break
			}
		}
	}
	a := &app{
		engine:       engine,
		hist:         hist,
		cfg:          cfg,
		board:        engine.Board,
		deviceID:     id.DeviceID,
		fingerprint:  identity.FormatFingerprint(id.Fingerprint),
		name:         engine.Name,
		listen:       listen,
		addrs:        addrs,
		paused:       cfg.Values.Paused,
		textOnly:     cfg.Values.TextOnly,
		maxImageText: strconv.Itoa(cfg.Values.MaxImageMB),
		page:         pageDevices,
		peer:         peer,
		status:       "未连接",
		histSel:      -1,
		pickSel:      -1,

		entries: hist.List(),
		devices: devices,
	}
	for _, dev := range devices {
		if dev.Peer.Addr == peer && peer != "" {
			a.targetPeerID = dev.ID
			break
		}
	}
	a.histList.Key = func(i int) any {
		shown := a.shown()
		if i < 0 || i >= len(shown) {
			return i
		}
		return shown[i].ID
	}
	a.histList.Label = func(i int) string {
		shown := a.shown()
		if i < 0 || i >= len(shown) {
			return ""
		}
		return entryTitle(shown[i])
	}
	a.histList.Selected = &a.histSel
	a.pickList.Key = func(i int) any {
		shown := a.filtered(a.pickQuery)
		if i < 0 || i >= len(shown) {
			return i
		}
		return shown[i].ID
	}
	a.pickList.Label = func(i int) string {
		shown := a.filtered(a.pickQuery)
		if i < 0 || i >= len(shown) {
			return ""
		}
		return entryTitle(shown[i])
	}
	a.pickList.Selected = &a.pickSel
	return a
}

func (a *app) open(ctx context.Context) {
	a.root = ctx
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:     "Snapover",
		Width:     880,
		Height:    560,
		MinWidth:  720,
		MinHeight: 440,
		Hidden:    mygo.App.WasOpenedAtLogin(),
		Content:   ui.View(a.view),
	})
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if a.quitting {
			return
		}
		e.PreventDefault()
		a.win.Hide()
	})
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "设置", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.show(pageSettings) }},
		{Label: "连接设备", Accelerator: "CmdOrCtrl+2", Click: func(*mygo.MenuItem, *mygo.Window) { a.show(pageDevices) }},
		{Label: "复制历史", Accelerator: "CmdOrCtrl+3", Click: func(*mygo.MenuItem, *mygo.Window) { a.show(pageHistory) }},
		{Label: "发送文件…", Accelerator: "CmdOrCtrl+4", Click: func(*mygo.MenuItem, *mygo.Window) { a.openFiles() }},
		{Role: mygo.RoleEditMenu},
	}))
	a.restoreLogin()
	a.initSendTo()
	a.startTray()
	a.rememberPasteTarget()
	go a.watchPasteTarget(ctx)
	mygo.App.OnDidResignActive(func() {
		time.AfterFunc(80*time.Millisecond, func() { mygo.RunOnMain(a.rememberPasteTarget) })
	})
	a.registerPickerShortcut()
	a.discoveryWake = make(chan struct{}, 1)
	a.discoveryScope.Store(a.selection().Scope())
	var lastDiscoveryError string
	go discovery.Run(ctx, discovery.Device{ID: a.deviceID, Name: a.name, Platform: a.engine.Platform, Addr: portOf(a.listen)}, a.listen, a.currentDiscoveryScope, a.discoveryWake, func(snapshot discovery.Snapshot) {
		errText := ""
		if snapshot.Err != nil {
			errText = snapshot.Err.Error()
		}
		if errText != "" && errText != lastDiscoveryError && a.engine.Log != nil {
			a.engine.Log.Warn("局域网发现失败", "message", snapshot.Message, "err", snapshot.Err)
		}
		lastDiscoveryError = errText
		a.win.Update(func() { a.applyDiscovery(snapshot) })
	})
	if a.peer != "" {
		a.connect()
	}
	if request := a.pendingFileRequest; request != nil {
		a.pendingFileRequest = nil
		a.handleFileRequest(request.paths, request.cold)
	}
}

func (a *app) show(page string) {
	if a.win == nil {
		a.page = page
		return
	}
	a.win.Update(func() { a.reveal(page) })
}

func (a *app) onNotice(n clipsync.Notice) {
	var added *history.Entry
	if (n.Kind == clipsync.NoticeLocal || n.Kind == clipsync.NoticeRemote) && n.Item.Kind != "" {
		dir := history.DirLocal
		peer := a.name
		if n.Kind == clipsync.NoticeRemote {
			dir = history.DirRemote
			peer = n.PeerName
		}
		entry, err := a.hist.Add(dir, peer, n.Item)
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
	case clipsync.NoticePairing:
		if n.Message != "" {
			a.status = n.Message
		}
		a.page = pageDevices
	case clipsync.NoticeStatus, clipsync.NoticeError:
		a.status = n.Message
		a.devices = a.engine.Peers.List()
		if link, up := a.engine.Link(); up && n.PeerID == link.ID && n.Kind == clipsync.NoticeStatus {
			for _, dev := range a.devices {
				if dev.ID == link.ID && a.targetPeerID != link.ID {
					a.targetPeerID = link.ID
					a.peer = dev.Peer.Addr
					a.connect()
					a.status = n.Message
				}
			}
		}
	case clipsync.NoticeLocal, clipsync.NoticeRemote:
		if n.Message != "" {
			a.flash = n.Message
		}
		if added != nil {
			a.refreshEntries()
		}
	case clipsync.NoticeFile:
		a.receiveMessage = receivedMessage(n)
		if n.FileState == "complete" {
			a.lastReceived = n.FilePath
			a.flash = a.receiveMessage
			a.receivedNotification(n)
		}
	}
	if a.fileWin != nil {
		a.fileWin.Invalidate()
	}
	a.refreshTray()
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(t.Background).Children(func() {
		ui.Column(c).Width(168).FillHeight().Background(t.Surface).Children(func() {
			ui.Text(c, "Snapover").Bold().Padding(16, 14, 8, 14)
			if ui.Sidebar(c, &a.page, func() {
				ui.SidebarItem(c, pageSettings, nil, "设置")
				ui.SidebarItem(c, pageDevices, nil, "连接设备")
				ui.SidebarItem(c, pageHistory, nil, "复制历史")
			}).Grow(1).Changed() {
				a.confirm = false
			}
			ui.Text(c, a.status).FontSize(12).TextColor(t.TextMuted).Padding(8, 14, 14, 14).MaxLines(3)
			if ui.Button(c, "发送文件…").Margin(0, 14, 14, 14).Clicked() {
				a.openFiles()
			}
		})
		ui.Column(c).Grow(1).FillHeight().Padding(20, 24).Gap(14).Children(func() {
			switch a.page {
			case pageDevices:
				ui.Scroll(c.Key("devices-scroll")).Grow(1).Gap(14).Children(func() { a.devicesPage(c) })
			case pageHistory:
				a.historyPage(c)
			default:
				ui.Scroll(c.Key("settings-scroll")).Grow(1).Gap(14).Children(func() { a.settingsPage(c) })
			}
		})
	})
}

func (a *app) settingsPage(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "设置").FontSize(22).Bold()
	ui.Text(c, "同步和开机启动在这里。添加或管理电脑请打开「连接设备」。").TextColor(t.TextMuted)

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

	section(c, "同步")
	if ui.Checkbox(c, &a.openAtLogin, "开机启动").Changed() {
		a.setOpenAtLogin(a.openAtLogin)
	}
	if ui.Checkbox(c, &a.paused, "暂停同步").Changed() {
		a.saveSettings()
	}
	if ui.Checkbox(c, &a.textOnly, "只同步文本").Changed() {
		a.saveSettings()
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "图片上限").TextColor(t.TextMuted)
		if ui.TextInput(c.Key("maxmb"), &a.maxImageText).Width(56).Changed() {
			a.saveSettings()
		}
		ui.Text(c, "MB，最大 20。超过上限的图片只留在本机历史里。").TextColor(t.TextMuted)
	})

	a.receiveSettings(c)
	section(c, "诊断")
	ui.Text(c, "连不上时，先确认对端已经启动。Windows 防火墙需要放行 TCP 47821，自动发现还需要 UDP 5353；macOS 需要允许 Snapover 访问本地网络。虚拟网卡不用于发现或连接。").TextColor(t.TextMuted)
	if ui.Button(c, "测试连接").Clicked() {
		a.probe()
	}
}

func (a *app) historyPage(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "复制历史").FontSize(22).Bold()
	ui.Text(c, pickerHint()+". 双击一条写回剪贴板。未置顶的记录保留 1000 条或 30 天。").TextColor(t.TextMuted)
	ui.TextInput(c.Key("query"), &a.query).Placeholder("搜索文字")
	shown := a.shown()
	if a.histSel >= len(shown) {
		a.histSel = -1
	}
	if a.historySearch.pending != nil {
		c.After(50 * time.Millisecond)
	}
	ui.Row(c).Gap(8).Children(func() {
		if ui.PrimaryButton(c, "写回剪贴板").Disabled(a.histSel < 0).Clicked() {
			a.pasteHistory()
		}
		if ui.Button(c, "置顶").Disabled(a.histSel < 0).Clicked() {
			a.pinSelected()
		}
		if ui.Button(c, "删除").Disabled(a.histSel < 0).Clicked() {
			a.deleteSelected()
		}
		label := "清空"
		if a.confirmClear {
			label = "再点一次，清空未置顶"
		}
		if ui.Button(c, label).Clicked() {
			a.clearHistory()
		}
	})
	if a.flash != "" {
		ui.Text(c, a.flash).FontSize(12).TextColor(t.TextMuted)
	}
	if len(a.entries) == 0 {
		ui.Text(c, "还没有记录。在这台电脑上复制一段文字，或等对端同步过来。").Padding(8, 0)
		return
	}
	if len(shown) == 0 {
		ui.Text(c, a.historySearch.emptyMessage()).TextColor(t.TextMuted)
		return
	}
	if ui.List(c, &a.histList, len(shown), func(i int) {
		entry := shown[i]
		ui.Row(c).Padding(8, 4).Gap(10).AlignItems(ui.Center).Children(func() {
			if entry.Kind == string(clip.KindImage) {
				if bmp := a.thumb(entry.ID); bmp != nil {
					ui.Image(c, bmp).Size(40, 40).Fit(ui.Cover).Radius(4)
				} else {
					ui.Box(c).Size(40, 40).Radius(4).Background(t.Surface)
				}
			}
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				title := entryTitle(entry)
				if entry.Pinned {
					title = "置顶  ·  " + title
				}
				ui.Text(c, title).SingleLine()
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
	a.stopDial()
	parent := a.root
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	a.dialCancel = cancel
	a.dialAddr = addr
	if a.targetPeerID == "" {
		for _, dev := range a.devices {
			if dev.Peer.Addr == addr {
				a.targetPeerID = dev.ID
				break
			}
		}
	}
	if link, up := a.engine.Link(); up && link.ID != a.targetPeerID {
		a.engine.Disconnect()
	}
	if !a.isPaired(a.targetPeerID) {
		a.engine.SetPairing(true)
	}
	a.status = "正在连接 " + addr
	seq := a.dialSeq
	peerID := a.targetPeerID
	go func() {
		clipsync.DialPeer(ctx, addr, peerID, a.engine)
		if a.win != nil && ctx.Err() == nil {
			a.win.Update(func() {
				if a.dialSeq == seq {
					a.dialCancel = nil
				}
			})
		}
	}()
}

func (a *app) forget(id string) {
	var addr string
	for _, dev := range a.devices {
		if dev.ID == id {
			addr = dev.Peer.Addr
			break
		}
	}
	if err := a.engine.Revoke(id); err != nil {
		a.status = "解除配对失败，请重试"
		return
	}
	if a.targetPeerID == id || (addr != "" && addr == a.dialAddr) {
		a.stopDial()
	}
	if a.targetPeerID == id {
		a.targetPeerID = ""
		a.peer = ""
	}
	a.confirmRemove = ""
	a.devices = a.engine.Peers.List()
	a.status = "已解除配对并断开连接。再次添加需要双方确认"
}

func (a *app) stopDial() {
	a.dialSeq++
	if a.dialCancel != nil {
		a.dialCancel()
		a.dialCancel = nil
	}
	a.dialAddr = ""
}

func (a *app) shown() []history.Entry {
	return a.historySearch.filter(a.root, a.hist, a.entries, a.query)
}

func (a *app) filtered(query string) []history.Entry {
	return a.pickerSearch.filter(a.root, a.hist, a.entries, query)
}

func matchesEntry(entry history.Entry, q string) bool { return history.Matches(entry, q) }

func (a *app) refreshEntries() {
	a.entries = a.hist.List()
	a.historySearch.reset()
	a.pickerSearch.reset()
	if a.thumbs == nil {
		return
	}
	live := make(map[string]bool, len(a.entries))
	for _, entry := range a.entries {
		live[entry.ID] = true
	}
	for _, id := range a.thumbs.Keys() {
		if !live[id] {
			a.thumbs.Remove(id)
		}
	}
}

func (a *app) selected() (history.Entry, bool) {
	shown := a.shown()
	if a.histSel < 0 || a.histSel >= len(shown) {
		return history.Entry{}, false
	}
	return shown[a.histSel], true
}

func (a *app) saveSettings() {
	n, err := strconv.Atoi(strings.TrimSpace(a.maxImageText))
	if err != nil {
		n = a.cfg.Values.MaxImageMB
	}
	if n < 1 {
		n = 1
	}
	if n > settings.DefaultMaxImageMB {
		n = settings.DefaultMaxImageMB
	}
	a.maxImageText = strconv.Itoa(n)
	a.cfg.Values.Paused = a.paused
	a.cfg.Values.TextOnly = a.textOnly
	a.cfg.Values.MaxImageMB = n
	if err := a.cfg.Save(); err != nil {
		a.status = "保存设置失败"
		return
	}
	a.engine.SetPolicy(a.paused, a.textOnly, n<<20)
	if a.paused {
		a.status = "已暂停同步"
	}
	a.refreshTray()
}

func (a *app) probe() {
	addr, err := normalizePeer(a.peer)
	if err != nil || addr == "" {
		a.status = "先填写对端地址"
		return
	}
	a.status = "正在测试 " + addr
	go func() {
		conn, bind, err := lan.DialTimeout(addr, 3*time.Second)
		msg := "端口通了。若仍不同步，确认双方已完成配对，并检查防火墙是否放行了入站 TCP " + defaultPort
		if bind != nil {
			msg = "已从 " + bind.String() + " 拨出，不走虚拟网卡。" + msg
		}
		if err != nil {
			msg = "连不上 " + addr + "。对端可能没启动，或防火墙没放行入站 TCP " + defaultPort
			if bind != nil {
				msg += "。已避开虚拟网卡，请填局域网地址"
			}
		} else {
			_ = conn.Close()
		}
		if a.win != nil {
			a.win.Update(func() { a.status = msg })
		}
	}()
}

func (a *app) pinSelected() {
	entry, ok := a.selected()
	if !ok {
		return
	}
	if err := a.hist.SetPinned(entry.ID, !entry.Pinned); err != nil {
		a.flash = "置顶失败"
		return
	}
	a.refreshEntries()
	a.flash = "已更新置顶"
}

func (a *app) deleteSelected() {
	entry, ok := a.selected()
	if !ok {
		return
	}
	if err := a.hist.Delete(entry.ID); err != nil {
		a.flash = "删除失败"
		return
	}
	if a.thumbs != nil {
		a.thumbs.Remove(entry.ID)
	}
	a.refreshEntries()
	a.histSel = -1
	a.flash = "已删除"
}

func (a *app) clearHistory() {
	if !a.confirmClear {
		a.confirmClear = true
		return
	}
	a.confirmClear = false
	if err := a.hist.Clear(); err != nil {
		a.flash = "清空失败"
		return
	}
	if a.thumbs != nil {
		a.thumbs.Purge()
	}
	a.refreshEntries()
	a.histSel = -1
	a.flash = "已清空未置顶的记录"
}

func (a *app) pasteHistory() {
	entry, ok := a.selected()
	if !ok {
		return
	}
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

const maxThumbCache = 128

func (a *app) thumb(id string) *ui.Bitmap {
	if a.thumbs == nil {
		a.thumbs, _ = simplelru.NewLRU[string, *ui.Bitmap](maxThumbCache, nil)
	}
	if bmp, ok := a.thumbs.Get(id); ok {
		return bmp
	}
	raw, err := a.hist.Thumb(id)
	if err != nil || len(raw) == 0 {
		a.thumbs.Add(id, nil)
		return nil
	}
	bmp, err := ui.DecodeBitmap(raw)
	if err != nil {
		a.thumbs.Add(id, nil)
		return nil
	}
	a.thumbs.Add(id, bmp)
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

func entryTitle(e history.Entry) string { return history.Title(e) }

func entryMeta(e history.Entry) string {
	from := "本机"
	if e.Dir == history.DirRemote {
		from = "对端"
	}
	if e.Peer != "" {
		from += " " + e.Peer
	}
	parts := []string{relTime(e.At), from}
	if e.SourceApp != "" {
		parts = append(parts, e.SourceApp)
	}
	parts = append(parts, formatSize(e.Size))
	return strings.Join(parts, "  ·  ")
}

func relTime(at time.Time) string {
	d := time.Since(at)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return at.Format("01-02 15:04")
	}
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
	var physical, virtual []string
	for _, addr := range lan.IPv4() {
		label := addr.Name + "  " + addr.IP.String() + ":" + port
		if addr.Virtual {
			virtual = append(virtual, label+"  ·  不用于连接")
			continue
		}
		physical = append(physical, label)
	}
	return append(physical, virtual...)
}
