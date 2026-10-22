package main

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hashicorp/golang-lru/v2/simplelru"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/discovery"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/keepawake"
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

	cfg                       *settings.File
	paused                    bool
	textOnly                  bool
	maxImageText              string
	maxTextText               string
	maxTextError              string
	shortcutText              string
	shortcutRecording         *shortcutRecording
	shortcutError             string
	registeredPickerShortcuts []string
	registeredCopyShortcuts   []string
	copyShortcutText          string
	copyShortcutError         string
	copyBusy                  bool
	keepAwake                 bool
	keepAwakeWorker           keepAwakeSession
	keepAwakeError            string
	syncMode                  int
	syncModeError             string
	historyMaxText            string
	historyCapacityError      string
	historyUsage              history.Usage
	historyUsageValid         bool
	historyUsageError         string
	historyUsageVersion       uint64
	historyUsagePending       chan historyUsageResult
	historyCleanupPending     chan historyCleanupResult
	confirmStorageClear       bool

	page            string
	peer            string
	query           string
	status          string
	syncStatus      string
	syncFailure     string
	syncSessionID   string
	flash           string
	confirm         bool
	confirmClear    bool
	showIdentity    bool
	showDiagnostics bool
	showNetwork     bool
	showAddress     bool
	showSyncLimits  bool
	historyKind     int
	shownEntries    []history.Entry
	shownSource     []history.Entry
	shownKind       int

	discovered       []discovery.Device
	discoveryMessage string
	discoveryScope   atomic.Value
	discoveryWake    chan struct{}
	segments         []segmentRow
	targetPeerID     string
	dialSeq          uint64
	deviceDetails    string
	confirmRemove    string

	devices           []clipsync.Device
	entries           []history.Entry
	histSel           int
	histList          ui.ListState
	thumbs            *simplelru.LRU[string, *ui.Bitmap]
	thumbBytes        int
	thumbPending      map[string]*thumbRequest
	thumbQueue        []*thumbRequest
	thumbActive       int
	thumbResults      chan thumbResult
	thumbUsedAt       time.Time
	thumbTimer        *time.Timer
	historySearch     historySearch
	pickerSearch      historySearch
	historyPaste      *pasteRequest
	pasteWorker       *pasteRequest
	queuedPaste       *pasteRequest
	notificationOnce  sync.Once
	notificationQueue chan fileNotification
	pasteSerial       atomic.Uint64

	win                *mygo.Window
	tray               *mygo.Tray
	picker             *mygo.Window
	pickPreview        pickerPreview
	pickLoading        bool
	pickOriginal       *mygo.Window
	quitting           bool
	sendToColdLaunch   bool
	openAtLogin        bool
	pickQuery          string
	pickSel            int
	pickList           ui.ListState
	pickFlash          string
	pickBusy           bool
	pickNeedsAccess    bool
	pasteTo            paste.Target
	dialCancel         context.CancelFunc
	dialAddr           string
	fileWin            *mygo.Window
	networkGuide       *networkGuide
	filePath           string
	fileSize           int64
	fileBusy           bool
	fileChoosing       bool
	fileSent           bool
	fileRecipient      string
	fileDone           int64
	fileMessage        string
	receiveMessage     string
	fileCommandHint    string
	fileWaiting        bool
	pendingFileRequest *fileRequest
	sendToBusy         bool
}

func newApp(engine *clipsync.Engine, hist *history.Store, cfg *settings.File, id *identity.Identity, listen string, addrs []string, peer string) *app {
	if cfg == nil {
		cfg = &settings.File{Values: settings.Values{MaxImageMB: settings.DefaultMaxImageMB}}
	}
	if cfg.Values.MaxTextMB == 0 {
		cfg.Values.MaxTextMB = settings.DefaultMaxTextMB
	}
	if cfg.Values.HistoryMaxMB <= 0 {
		cfg.Values.HistoryMaxMB = settings.DefaultHistoryMB
	}
	engine.SetTextLimit(cfg.Values.MaxTextMB << 20)
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
		engine:         engine,
		hist:           hist,
		cfg:            cfg,
		board:          engine.Board,
		deviceID:       id.DeviceID,
		fingerprint:    identity.FormatFingerprint(id.Fingerprint),
		name:           engine.Name,
		listen:         listen,
		addrs:          addrs,
		paused:         cfg.Values.Paused,
		textOnly:       cfg.Values.TextOnly,
		keepAwake:      cfg.Values.KeepAwake,
		maxImageText:   strconv.Itoa(cfg.Values.MaxImageMB),
		maxTextText:    strconv.Itoa(cfg.Values.MaxTextMB),
		historyMaxText: strconv.Itoa(cfg.Values.HistoryMaxMB),
		page:           pageDevices,
		peer:           peer,
		status:         "未连接",
		histSel:        -1,
		pickSel:        -1,

		entries: hist.List(),
		devices: devices,
	}
	if cfg.Values.ManualSync {
		a.syncMode = 1
	}
	a.copyShortcutText, _ = configuredCopyShortcut(cfg.Values.CopyShortcut)
	a.copyShortcutText = displayPickerShortcut(a.copyShortcutText)
	a.shortcutText = cfg.Values.PickerShortcut
	if a.shortcutText == "" {
		a.shortcutText = pickerShortcuts()[0]
	}
	a.shortcutText = displayPickerShortcut(a.shortcutText)
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
	// Windows inherits the application menu in every ordinary window.
	mygo.App.SetMenu(nil)
	if runtime.GOOS == "darwin" {
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Label: "设置", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.show(pageSettings) }},
			{Label: "连接设备", Accelerator: "CmdOrCtrl+2", Click: func(*mygo.MenuItem, *mygo.Window) { a.show(pageDevices) }},
			{Label: "复制历史", Accelerator: "CmdOrCtrl+3", Click: func(*mygo.MenuItem, *mygo.Window) { a.show(pageHistory) }},
			{Label: "发送文件…", Accelerator: "CmdOrCtrl+4", Click: func(*mygo.MenuItem, *mygo.Window) { a.openFiles() }},
			{Role: mygo.RoleEditMenu},
		}))
	}
	a.restoreLogin()
	a.restoreKeepAwake()
	a.initSendTo()
	a.startTray()
	if (!mygo.App.WasOpenedAtLogin() && !a.sendToColdLaunch && a.pendingFileRequest == nil) || a.tray == nil {
		a.openMainWindow()
	}
	a.rememberPasteTarget()
	a.startPasteTargetWatch(ctx)
	a.registerPickerShortcut()
	a.registerCopyShortcut()
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
		a.update(func() { a.applyDiscovery(snapshot) })
	})
	if a.peer != "" {
		a.connect()
	}
	if request := a.pendingFileRequest; request != nil {
		a.pendingFileRequest = nil
		a.handleFileRequest(request.paths, request.cold)
	}
}

func (a *app) openMainWindow() {
	if a.win != nil || a.quitting {
		return
	}
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:     "Snapover",
		Width:     960,
		Height:    640,
		MinWidth:  720,
		MinHeight: 440,
		StateKey:  "main",
		Content:   ui.View(a.view),
	})
	a.win = win
	win.OnBlur(func() { a.cancelShortcutRecording(); win.Invalidate() })
	win.OnClosed(func() {
		a.cancelShortcutRecording()
		if a.pickPreview.parent == win {
			a.closePickPreview()
		}
		a.win = nil
		a.historySearch.reset()
		a.shownSource, a.shownEntries = nil, nil
		// ListState holds its window host, including the closed UI's resources.
		a.histList = ui.ListState{Key: a.histList.Key, Label: a.histList.Label, Selected: &a.histSel}
		a.confirm, a.confirmClear, a.confirmStorageClear = false, false, false
		if r := a.historyPaste; r != nil && r.picker == nil {
			a.cancelHistoryPaste()
		}
		if a.picker == nil {
			a.releaseThumbnails()
		}
	})
}

// Background work outlives every window. Dispatch state changes through the
// application, then redraw whichever windows are still open.
func (a *app) update(fn func()) {
	mygo.RunOnMain(func() {
		fn()
		if a.win != nil {
			a.win.Invalidate()
		}
		if a.picker != nil {
			a.picker.Invalidate()
		}
		if a.fileWin != nil {
			a.fileWin.Invalidate()
		}
		a.refreshNetworkGuide()
	})
}

func (a *app) show(page string) {
	a.update(func() { a.reveal(page) })
}

func (a *app) onNotice(n clipsync.Notice) error {
	var added *history.Entry
	var storageErr error
	if (n.Kind == clipsync.NoticeLocal || n.Kind == clipsync.NoticeRemote) && n.Item.Kind != "" {
		dir := history.DirLocal
		peer := a.name
		if n.Kind == clipsync.NoticeRemote {
			dir = history.DirRemote
			peer = n.PeerName
		}
		entry, err := a.hist.Add(dir, peer, n.Item)
		if entry.ID != "" {
			added = &entry
		}
		if err != nil {
			storageErr = err
			n.Kind = clipsync.NoticeError
			n.Message = "写入或清理历史失败：" + err.Error()
		}
	}
	a.update(func() { a.apply(n, added) })
	return storageErr
}

func (a *app) apply(n clipsync.Notice, added *history.Entry) {
	if added != nil {
		a.refreshEntries()
	}
	switch n.Kind {
	case clipsync.NoticePairing:
		if n.Message != "" {
			a.status = n.Message
		}
		a.page = pageDevices
	case clipsync.NoticeStatus, clipsync.NoticeError:
		if n.Kind == clipsync.NoticeStatus {
			a.clearSyncFailure()
			if link, up := a.engine.Link(); up && link.SessionID != a.syncSessionID {
				a.syncStatus = ""
			}
		}
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
	case clipsync.NoticeSync:
		if n.SessionID != "" {
			link, up := a.engine.Link()
			if !up || link.SessionID != n.SessionID {
				return
			}
		}
		a.clearSyncFailure()
		a.syncSessionID = n.SessionID
		a.syncStatus = n.Message
		if n.ClipboardState == "complete" {
			a.syncStatus = "已确认写入对端剪贴板"
		}
		if n.ClipboardState != "complete" {
			a.flash = n.Message
			a.syncFailure = n.Message
		}
	case clipsync.NoticeFile:
		a.receiveMessage = receivedMessage(n)
		if n.FileState == "complete" || n.FileState == "error" {
			a.flash = a.receiveMessage
		}
		if n.FileState == "complete" {
			a.receivedNotification(n)
		}
	}
	if a.fileWin != nil {
		a.fileWin.Invalidate()
	}
	a.refreshTray()
}

func (a *app) view(c *ui.Context) {
	if a.page != pageSettings {
		a.cancelShortcutRecording()
	}
	a.windowShortcuts(c)
	a.pollHistoryStorage()
	if a.historyCleanupPending != nil {
		c.After(50 * time.Millisecond)
	}
	a.drainThumbnails()
	t := c.Theme()
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(t.Background).Children(func() {
		ui.Column(c).Width(184).FillHeight().Padding(12, 8).Gap(12).Background(t.Surface).Children(func() {
			ui.Column(c).Padding(10, 12, 16, 12).Gap(4).Children(func() {
				ui.Text(c, "Snapover").FontSize(18).Bold()
				ui.Text(c, appVersionLabel()).FontSize(11).TextColor(t.TextMuted).SingleLine()
				ui.Text(c, a.name).FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
			if ui.Sidebar(c, &a.page, func() {
				ui.SidebarItem(c, pageSettings, settingsIcon, "设置").Transition(navMotion)
				ui.SidebarItem(c, pageDevices, deviceIcon, "连接设备").Transition(navMotion)
				ui.SidebarItem(c, pageHistory, historyIcon, "复制历史").Transition(navMotion)
			}).Grow(1).Changed() {
				a.confirm = false
			}
			ui.Column(c).Padding(12).Gap(8).Children(func() {
				label := "未连接设备"
				link, up := a.engine.Link()
				if up {
					label = "已连接 · " + displayName(link.Name, link.ID)
				}
				if a.paused {
					label = "同步已暂停"
				}
				ui.Text(c, label).FontSize(12).Bold().SingleLine().ContentTransition(ui.ContentTransition{})
				if status := a.displayStatus(); status != "" && status != "未连接" && status != "已连接" && status != "已连接 "+link.Name {
					ui.Text(c, status).FontSize(11).TextColor(t.TextMuted).MaxLines(3).ContentTransition(ui.ContentTransition{})
				}
			})
			if ui.Button(c, "").Label("发送文件").Children(func() {
				ui.Icon(c, sendIcon)
				ui.Text(c, "发送文件…")
			}).Margin(0, 6, 4, 6).Clicked() {
				a.openFiles()
			}
		})
		ui.Column(c.Key("page-host")).Grow(1).FillHeight().Children(func() {
			ui.Column(c.Key(a.page)).Fill().Padding(28).Gap(20).Transition(pageMotion).Children(func() {
				switch a.page {
				case pageDevices:
					ui.Scroll(c.Key("devices-scroll")).Grow(1).Gap(20).Children(func() { a.devicesPage(c) })
				case pageHistory:
					a.historyPage(c)
				default:
					ui.Scroll(c.Key("settings-scroll")).Grow(1).Gap(20).Children(func() { a.settingsPage(c) })
				}
			})
		})
	})
	if a.page != pageHistory && a.pickPreview.parent == a.win {
		a.closePickPreview()
	}
}

func (a *app) settingsPage(c *ui.Context) {
	t := c.Theme()
	pageHeading(c, "设置")
	settingsGroup(c, "剪贴板同步", func() {
		a.syncModeSettings(c)
		ui.Divider(c)
		if settingSwitch(c, &a.paused, "暂停同步", "仍保留本机复制历史。") {
			a.saveSettings()
		}
		ui.Divider(c)
		if settingSwitch(c, &a.textOnly, "只同步文本", "") {
			a.saveSettings()
		}
		ui.Divider(c)
		ui.Collapsible(c, "大小限制", &a.showSyncLimits, func() {
			ui.Column(c).Gap(14).Padding(12, 0).Children(func() {
				ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(4).Children(func() {
						ui.Text(c, "文本大小上限").Bold()
						ui.Text(c, "1–20 MB，超限不保存、不收发。").FontSize(12).TextColor(t.TextMuted)
					})
					if ui.TextInput(c.Key("maxtextmb"), &a.maxTextText).Label("文本大小上限，MB").Width(56).Submitted() {
						a.saveTextLimit()
					}
					ui.Text(c, "MB").TextColor(t.TextMuted)
					if ui.Button(c, "应用文本上限").Clicked() {
						a.saveTextLimit()
					}
				})
				if a.maxTextError != "" {
					ui.Text(c, a.maxTextError).FontSize(12).TextColor(t.Danger)
				}
				ui.Divider(c)
				ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(4).Children(func() {
						ui.Text(c, "图片大小上限").Bold()
						ui.Text(c, "最多 20 MB，超限仅保留本机历史。").FontSize(12).TextColor(t.TextMuted)
					})
					if ui.TextInput(c.Key("maxmb"), &a.maxImageText).Label("图片大小上限，MB").Width(56).Submitted() {
						a.saveSettings()
					}
					ui.Text(c, "MB").TextColor(t.TextMuted)
					if ui.Button(c, "应用").Clicked() {
						a.saveSettings()
					}
				})
			})
		})
	})
	settingsGroup(c, "复制并同步快捷键", func() { a.copyShortcutSettings(c) })
	settingsGroup(c, "快速历史快捷键", func() { a.shortcutSettings(c) })
	settingsGroup(c, "历史存储", func() { a.historyStorageSettings(c) })
	settingsGroup(c, "启动", func() {
		if settingSwitch(c, &a.openAtLogin, "开机启动", "") {
			a.setOpenAtLogin(a.openAtLogin)
		}
	})
	if keepawake.Supported {
		settingsGroup(c, "保持唤醒", func() { a.keepAwakeSettings(c) })
	}
	settingsGroup(c, "接收文件", func() { a.receiveSettings(c) })
	settingsGroup(c, "本机与诊断", func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, deviceIcon).FontSize(24).TextColor(t.TextMuted)
			ui.Column(c).Gap(4).Grow(1).Children(func() {
				ui.Text(c, a.name).Bold()
			})
		})
		ui.Collapsible(c, "设备信息", &a.showIdentity, func() {
			ui.Column(c).Padding(12, 0).Gap(10).Children(func() {
				kv(c, "设备 ID", a.deviceID)
				ui.Text(c, "证书指纹").FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, a.fingerprint).Selectable().FontSize(12).FontFeatures("tnum")
				if len(a.addrs) == 0 {
					kv(c, "地址", "没有可用的局域网 IPv4")
				}
				for _, addr := range a.addrs {
					kv(c, "地址", addr)
				}
			})
		})
		ui.Collapsible(c, "连接诊断", &a.showDiagnostics, func() {
			ui.Column(c).Padding(12, 0).Gap(12).Children(func() {
				ui.Text(c, "确认对端已启动，并允许本地网络访问。Windows 防火墙需放行 TCP 47821 和 UDP 5353；虚拟网卡不用于连接。").FontSize(12).TextColor(t.TextMuted)
				if ui.Button(c, "测试连接").Clicked() {
					a.probe()
				}
			})
		})
	})
}

func (a *app) historyPage(c *ui.Context) {
	a.pollHistoryPaste(c)
	var hoverID string
	var hoverBounds ui.Rect
	t := c.Theme()
	pageHeading(c, "复制历史")
	if a.historyUnavailable(c) {
		return
	}
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		if ui.TextInput(c.Key("query"), &a.query).Label("搜索复制历史").Placeholder("搜索内容或来源").Grow(1).Changed() {
			a.closePickPreview()
			a.histSel = -1
			a.histList.ScrollTo(0, ui.Start)
		}
		label := "清空未置顶"
		if a.confirmClear {
			label = "确认清空"
		}
		if ui.Button(c, label).Disabled(len(a.entries) == 0).Clicked() {
			a.clearHistory()
		}
		if a.confirmClear && ui.Button(c, "取消").Clicked() {
			a.confirmClear = false
		}
	})
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		if ui.Segmented(c, &a.historyKind, "全部", "文本", "图片", "置顶").Label("历史类型").Changed() {
			a.closePickPreview()
			a.histSel = -1
			a.histList.ScrollTo(0, ui.Start)
		}
		ui.Spacer(c)
		ui.Textf(c, "%d 条记录", len(a.shown())).FontSize(12).TextColor(t.TextMuted).
			ContentTransition(ui.ContentTransition{Numeric: true})
	})
	shown := a.shown()
	if a.histSel >= len(shown) {
		a.histSel = -1
	}
	if a.historySearch.pending != nil {
		c.After(50 * time.Millisecond)
	}
	ui.Column(c).Grow(1).Radius(10).Border(1, t.Border).Clip().Children(func() {
		switch {
		case len(a.entries) == 0:
			emptyState(c, historyIcon, "还没有复制历史", "复制文字或图片后会自动保存。")
		case len(shown) == 0:
			message := "试试其他关键词，或切换到「全部」。"
			if a.query != "" {
				message = a.historySearch.emptyMessage()
			}
			emptyState(c, searchIcon, "没有匹配的记录", message)
		default:
			if ui.List(c, &a.histList, len(shown), func(i int) {
				entry := shown[i]
				sel := i == a.histSel
				row := ui.Row(c).Padding(12, 10).Gap(12).AlignItems(ui.Center)
				var title ui.Element
				titleText := entryTitle(entry)
				row.Children(func() {
					a.historyThumb(c, entry, t, sel, 40)
					ui.Column(c).Grow(1).Gap(5).Children(func() {
						title = ui.Text(c, titleText).SingleLine()
						ui.Row(c).Gap(6).Children(func() {
							if entry.Pinned {
								ui.Icon(c, pinIcon).FontSize(12).TextColor(rowMetaColor(t, sel))
							}
							ui.Text(c, historySource(entry)).FontSize(12).TextColor(rowMetaColor(t, sel)).SingleLine()
						})
					})
					ui.Column(c).Gap(5).AlignItems(ui.End).Children(func() {
						ui.Text(c, relTime(entry.At)).FontSize(11).TextColor(rowMetaColor(t, sel)).SingleLine()
						ui.Text(c, formatSize(entry.Size)).FontSize(11).TextColor(rowMetaColor(t, sel)).SingleLine()
					})
				})
				if row.Hovered() && rowNeedsPreview(c, entry, titleText, title) {
					hoverID, hoverBounds = entry.ID, row.Bounds()
				}
			}).Grow(1).Submitted() {
				a.pasteHistory()
			}
		}
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.PrimaryButton(c, "复制").Tooltip("复制到剪贴板，也可双击记录。").Disabled(a.histSel < 0 || a.historyPaste != nil || a.pasteWorker != nil || a.historyCleanupPending != nil).Clicked() {
			a.pasteHistory()
		}
		ui.Toolbar(c, func() {
			label := "置顶"
			if entry, ok := a.selected(); ok && entry.Pinned {
				label = "取消置顶"
			}
			if ui.Button(c, label).Disabled(a.histSel < 0 || a.historyCleanupPending != nil).Clicked() {
				a.pinSelected()
			}
			if ui.Button(c, "删除").Disabled(a.histSel < 0 || a.historyCleanupPending != nil).Clicked() {
				a.deleteSelected()
			}
		}).Label("记录操作")
		ui.Spacer(c)
		if a.flash != "" {
			ui.Text(c, a.flash).FontSize(12).TextColor(t.TextMuted).SingleLine()
		}
	})
	a.updatePickPreview(c, a.win, hoverID, hoverBounds)
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
		if ctx.Err() == nil {
			a.update(func() {
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
	entries := a.historySearch.filter(a.root, a.hist, a.entries, a.query)
	if a.historyKind == 0 {
		return entries
	}
	if a.shownKind == a.historyKind && len(entries) == len(a.shownSource) &&
		(len(entries) == 0 || &entries[0] == &a.shownSource[0]) {
		return a.shownEntries
	}
	shown := make([]history.Entry, 0, len(entries))
	for _, entry := range entries {
		if (a.historyKind == 1 && entry.Kind == string(clip.KindText)) ||
			(a.historyKind == 2 && entry.Kind == string(clip.KindImage)) ||
			(a.historyKind == 3 && entry.Pinned) {
			shown = append(shown, entry)
		}
	}
	a.shownSource, a.shownEntries, a.shownKind = entries, shown, a.historyKind
	return shown
}

func (a *app) filtered(query string) []history.Entry {
	return a.pickerSearch.filter(a.root, a.hist, a.entries, query)
}

func matchesEntry(entry history.Entry, q string) bool { return history.Matches(entry, q) }

func (a *app) refreshEntries() {
	a.historyUsageValid = false
	a.historyUsageVersion++
	a.entries = a.hist.List()
	if a.pickPreview.hover.id != "" {
		found := false
		for _, entry := range a.entries {
			if entry.ID == a.pickPreview.hover.id {
				found = true
				break
			}
		}
		if !found {
			a.closePickPreview()
		}
	}
	a.shownSource, a.shownEntries = nil, nil
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
	for id := range a.thumbPending {
		if !live[id] {
			delete(a.thumbPending, id)
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
	previousPaused := a.cfg.Values.Paused
	previousTextOnly := a.cfg.Values.TextOnly
	previousMax := a.cfg.Values.MaxImageMB
	n, err := strconv.Atoi(strings.TrimSpace(a.maxImageText))
	if err != nil {
		n = previousMax
	}
	if n < 1 {
		n = 1
	}
	if n > settings.DefaultMaxImageMB {
		n = settings.DefaultMaxImageMB
	}
	a.cfg.Values.Paused = a.paused
	a.cfg.Values.TextOnly = a.textOnly
	a.cfg.Values.MaxImageMB = n
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values.Paused = previousPaused
		a.cfg.Values.TextOnly = previousTextOnly
		a.cfg.Values.MaxImageMB = previousMax
		a.paused = previousPaused
		a.textOnly = previousTextOnly
		a.maxImageText = strconv.Itoa(previousMax)
		a.status = "保存设置失败，设置未生效，请检查数据目录权限后重试"
		return
	}
	a.maxImageText = strconv.Itoa(n)
	a.engine.SetPolicy(a.paused, a.textOnly, n<<20)
	if a.paused {
		a.status = "已暂停同步"
	}
	a.refreshTray()
}

func (a *app) saveTextLimit() {
	n, err := strconv.Atoi(strings.TrimSpace(a.maxTextText))
	if err != nil || n < 1 || n > settings.MaxTextMB {
		a.maxTextError = fmt.Sprintf("文本大小上限须为 1–%d 的整数，单位 MB", settings.MaxTextMB)
		return
	}
	previous := a.cfg.Values.MaxTextMB
	a.cfg.Values.MaxTextMB = n
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values.MaxTextMB = previous
		a.maxTextError = "文本上限保存失败，请检查数据目录权限后重试"
		return
	}
	a.engine.SetTextLimit(n << 20)
	a.maxTextText = strconv.Itoa(n)
	a.maxTextError = ""
	a.status = fmt.Sprintf("文本大小上限已设为 %d MB", n)
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
		a.update(func() { a.status = msg })
	}()
}

func (a *app) pinSelected() {
	entry, ok := a.selected()
	if !ok {
		return
	}
	a.startHistoryMutation(func() error { return a.hist.SetPinned(entry.ID, !entry.Pinned) }, "已更新置顶", false)
}

func (a *app) deleteSelected() {
	entry, ok := a.selected()
	if !ok {
		return
	}
	a.startHistoryMutation(func() error { return a.hist.Delete(entry.ID) }, "已删除", false)
}

func (a *app) clearHistory() {
	if !a.confirmClear {
		a.confirmClear = true
		return
	}
	a.confirmClear = false
	a.startHistoryCleanup(true, 0)
}

func (a *app) pasteHistory() {
	entry, ok := a.selected()
	if !ok {
		return
	}
	a.startHistoryPaste(entry.ID, nil)
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

// rowMetaColor is the color of a history row's secondary line. A chosen row
// sits on the accent fill, where the usual muted gray disappears, so there it
// borrows the accent's text color instead.
func rowMetaColor(t *ui.Theme, selected bool) ui.Color {
	if selected {
		return t.AccentText.Alpha(0.85)
	}
	return t.TextMuted
}

// rowThumbPlaceholder fills the thumb box while an image still loads.
func rowThumbPlaceholder(t *ui.Theme, selected bool) ui.Color {
	if selected {
		return t.AccentText.Alpha(0.25)
	}
	return t.Surface
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

func (a *app) clearSyncFailure() {
	if a.syncFailure != "" && a.flash == a.syncFailure {
		a.flash = ""
	}
	a.syncFailure = ""
}

func (a *app) displayStatus() string {
	if a.syncFailure != "" && !a.paused {
		return a.syncFailure
	}
	return a.status
}
