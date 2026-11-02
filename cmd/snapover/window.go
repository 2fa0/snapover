package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/discovery"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/keepawake"
	"github.com/2fa0/snapover/internal/lan"
	"github.com/2fa0/snapover/internal/paste"
	"github.com/2fa0/snapover/internal/settings"
	clipsync "github.com/2fa0/snapover/internal/sync"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hashicorp/golang-lru/v2/simplelru"
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
	systemLang                string
	languageError             string
	paused                    bool
	textOnly                  bool
	maxImageText              string
	maxTextText               string
	maxTextError              string
	maxFileText               string
	maxFileError              string
	shortcutText              string
	shortcutRecording         *shortcutRecording
	shortcutError             string
	registeredPickerShortcuts []string
	registeredCopyShortcuts   []string
	copyShortcutText          string
	copyShortcutError         string
	copyBusy                  bool
	keepAwake                 bool
	allowTailscale            bool
	tailscaleError            string
	keepAwakeWorker           keepAwakeSession
	keepAwakeError            string
	syncMode                  int
	syncModeError             string
	historyMaxText            string
	historyCapacityError      string
	updatesEnabled            bool
	autoUpdate                bool
	lastCheck                 time.Time
	historyUsage              history.Usage
	historyUsageValid         bool
	historyUsageError         string
	historyUsageVersion       uint64
	historyUsagePending       chan historyUsageResult
	historyCleanupPending     chan historyCleanupResult
	showDiskUsage             bool
	diskUsage                 diskUsageResult
	diskUsageValid            bool
	diskUsagePending          chan diskUsageResult
	diskUsageCancel           context.CancelFunc

	page                string
	peer                string
	startupPeerExplicit bool
	query               string
	status              string
	syncStatus          string
	syncFailure         string
	syncSessionID       string
	flash               string
	pendingToast        string
	confirm             bool
	confirmClear        bool
	showIdentity        bool
	showDiagnostics     bool
	showNetwork         bool
	showAddress         bool
	showSyncLimits      bool
	showReceiveFolder   bool
	historyKind         int
	shownEntries        []history.Entry
	shownSource         []history.Entry
	shownKind           int

	discovered        []discovery.Device
	discoveryMessage  string
	discoveryScope    atomic.Value
	discoveryWake     chan struct{}
	segments          []segmentRow
	targetPeerID      string
	coldTargetID      string
	addressRoute      int
	editTailscaleID   string
	editTailscaleAddr string
	dialTarget        *clipsync.DialTarget
	dialProposed      lan.Endpoint
	probeCancel       context.CancelFunc
	probeSeq          uint64
	dialSeq           uint64
	deviceDetails     string
	confirmRemove     string

	devices           []clipsync.Device
	entries           []history.Entry
	histSel           int
	histList          ui.ListState
	logo              *ui.Bitmap
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

	win                 *mygo.Window
	tray                *mygo.Tray
	trayMenuOpen        bool
	menuBarIconError    string
	menuBarPreviews     [3]*ui.Bitmap
	menuBarPreviewDark  bool
	picker              *mygo.Window
	pickPreview         pickerPreview
	pickLoading         bool
	pickOriginal        *mygo.Window
	quitting            bool
	sendToColdLaunch    bool
	openAtLogin         bool
	pickQuery           string
	pickSel             int
	pickRestore         bool
	pickRestoreID       string
	pickRestoreIndex    int
	pickMutationPending bool
	pickList            ui.ListState
	pickFlash           string
	pickBusy            bool
	pickNeedsAccess     bool
	// The picker's own size and place, in DIPs: pickSizeW and pickSizeH are
	// what the window shows, pickDragW and pickDragH the size the running
	// resize is heading for, before rounding. pickPlaced says the record was
	// read back from the window. A move starts at the place the window had
	// then, pickX and pickY, and the pointer's, pickGrab.
	pickSizeW          int
	pickSizeH          int
	pickDragW          float32
	pickDragH          float32
	pickResizing       bool
	pickPlaced         bool
	pickMoving         bool
	pickGrab           mygo.Point
	pickX              int
	pickY              int
	pickRect           mygo.Rectangle
	pickScale          float64
	pickArea           mygo.Rectangle
	pasteTo            paste.Target
	dialCancel         context.CancelFunc
	dialAddr           string
	fileWin            *mygo.Window
	networkGuide       *networkGuide
	filePaths          []string
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
	if cfg.Values.MaxFileMB <= 0 {
		cfg.Values.MaxFileMB = settings.DefaultMaxFileMB
	}
	if cfg.Values.HistoryMaxMB <= 0 {
		cfg.Values.HistoryMaxMB = settings.DefaultHistoryMB
	}
	engine.SetAllowTailscale(cfg.Values.AllowTailscale)
	engine.SetTextLimit(cfg.Values.MaxTextMB << 20)
	engine.SetFileLimit(cfg.Values.MaxFileMB << 20)
	devices := engine.Peers.List()
	explicitPeer := peer != ""
	if peer == "" {
		for _, dev := range devices {
			if endpoints := dev.Peer.Endpoints(true); len(endpoints) > 0 {
				peer = endpoints[0].Addr
				break
			}
		}
	}
	a := &app{
		engine:              engine,
		hist:                hist,
		cfg:                 cfg,
		systemLang:          settings.LangZH,
		board:               engine.Board,
		deviceID:            id.DeviceID,
		fingerprint:         identity.FormatFingerprint(id.Fingerprint),
		name:                engine.Name,
		listen:              listen,
		addrs:               addrs,
		paused:              cfg.Values.Paused,
		textOnly:            cfg.Values.TextOnly,
		keepAwake:           cfg.Values.KeepAwake,
		allowTailscale:      cfg.Values.AllowTailscale,
		maxImageText:        strconv.Itoa(cfg.Values.MaxImageMB),
		maxTextText:         strconv.Itoa(cfg.Values.MaxTextMB),
		maxFileText:         strconv.FormatInt(cfg.Values.MaxFileMB, 10),
		historyMaxText:      strconv.Itoa(cfg.Values.HistoryMaxMB),
		page:                pageDevices,
		peer:                peer,
		startupPeerExplicit: explicitPeer,
		status:              "未连接",
		histSel:             -1,
		pickSel:             -1,

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
		if dev.Peer.HasAddress(peer) {
			a.targetPeerID = dev.ID
			a.coldTargetID = dev.ID
			if dev.Peer.Endpoint(peer).Route == lan.RouteTailscale {
				a.addressRoute = 1
			}
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
		return a.entryTitle(shown[i])
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
		return a.entryTitle(shown[i])
	}
	a.pickList.Selected = &a.pickSel
	return a
}

// windowAppMenu 是 macOS 的窗口菜单栏：应用菜单里放系统惯用的「设置…」，页面切换留给侧栏，
// 不在菜单栏里重复一份。
func (a *app) windowAppMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			mygo.Separator(),
			{Label: a.tr("设置…"), Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.show(pageSettings) }},
			mygo.Separator(),
			{Role: mygo.RoleServices},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Role: mygo.RoleEditMenu},
	})
}

func (a *app) open(ctx context.Context) {
	a.root = ctx
	a.systemLang = languageFromTags([]string{mygo.App.Locale()})
	mygo.App.OnLocaleChanged(a.localeChanged)
	// Windows inherits the application menu in every ordinary window.
	mygo.App.SetMenu(nil)
	if runtime.GOOS == "darwin" {
		mygo.App.SetMenu(a.windowAppMenu())
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
		Title:         "Snapover",
		Width:         960,
		Height:        640,
		MinWidth:      720,
		MinHeight:     440,
		StateKey:      "main",
		TitleBarStyle: mygo.TitleBarHiddenInset,
		// The header row is as tall as the window controls, rather than the
		// room AppKit leaves above the content: the traffic lights go where
		// this strip ends, so the sidebar starts right below them instead of
		// after a band of empty title bar.
		TrafficLightPosition: &mygo.Point{X: 18, Y: 12},
		TitleBarHeight:       mainTitleBarHeight,
		Content:              ui.View(a.view),
	})
	a.win = win
	win.OnBlur(func() { a.cancelShortcutRecording(); win.Invalidate() })
	win.OnClosed(func() {
		a.cancelShortcutRecording()
		a.cancelDiskUsage()
		if a.pickPreview.parent == win {
			a.closePickPreview()
		}
		a.win = nil
		a.menuBarPreviews = [3]*ui.Bitmap{}
		a.historySearch.reset()
		a.shownSource, a.shownEntries = nil, nil
		// ListState holds its window host, including the closed UI's resources.
		a.histList = ui.ListState{Key: a.histList.Key, Label: a.histList.Label, Selected: &a.histSel}
		a.confirm, a.confirmClear = false, false
		a.pendingToast = ""
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
		var entry history.Entry
		var err error
		if n.Item.Kind == clip.KindFiles {
			ctx := a.root
			if ctx == nil {
				ctx = context.Background()
			}
			entry, err = a.hist.AddFiles(ctx, dir, peer, n.Item, a.engine.FileLimit())
		} else {
			entry, err = a.hist.Add(dir, peer, n.Item)
		}
		if errors.Is(err, context.Canceled) {
			return nil
		}
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
	if n.DialSeq != 0 && n.DialSeq != a.dialSeq {
		return
	}
	if n.Kind == clipsync.NoticeDial && (n.DialSeq != a.dialSeq || a.dialTarget == nil) {
		return
	}
	if n.SessionID != "" && n.Kind == clipsync.NoticeStatus {
		link, up := a.engine.Link()
		if (up && link.SessionID != n.SessionID) || (!up && n.Addr != "") {
			return
		}
	}
	switch n.Kind {
	case clipsync.NoticeDial:
		if !a.engine.Connected() {
			a.dialAddr = n.Addr
			a.status = n.Message
		}
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
			if a.targetPeerID != link.ID {
				a.targetPeerID = link.ID
				if p, ok := a.engine.Peers.Get(link.ID); ok {
					a.peer = p.Addr
				}
			}
			if a.dialTarget != nil && a.dialTarget.PeerID() == "" {
				a.dialTarget.BindPeer(link.ID)
			}
			if a.dialTarget == nil || a.dialTarget.PeerID() != link.ID {
				a.connect()
			} else {
				a.dialProposed = lan.Endpoint{}
				a.refreshConnectionCandidates()
			}
			a.status = n.Message
			if a.editTailscaleID == link.ID && link.Route == lan.RouteTailscale {
				a.editTailscaleID, a.editTailscaleAddr = "", ""
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
		if n.ClipboardState != "complete" && n.ClipboardState != "sending" {
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
	if a.pendingToast != "" {
		c.Toast(a.tr(a.pendingToast))
		a.pendingToast = ""
	}
	a.windowShortcuts(c)
	a.pollHistoryStorage()
	if a.historyCleanupPending != nil {
		c.After(50 * time.Millisecond)
	}
	a.drainThumbnails()
	t := c.Theme()
	bar := c.TitleBar()
	sideTop := float32(12)
	if bar.Height > 0 {
		sideTop = 0
	}
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(t.Background).Children(func() {
		ui.Column(c).Width(184).FillHeight().Padding(sideTop, 8, 12, 8).Gap(12).Background(t.Surface).Children(func() {
			if bar.Height > 0 {
				ui.Box(c).Height(bar.Height).Shrink(0).DragWindow()
			}
			ui.Column(c).Padding(8, 12, 14, 12).Children(func() {
				ui.Row(c).Gap(9).AlignItems(ui.Center).Children(func() {
					if logo := a.sidebarLogo(); logo != nil {
						ui.Image(c, logo).Size(sidebarLogoSize, sidebarLogoSize).Fit(ui.Contain)
					}
					ui.Text(c, "Snapover").FontSize(18).Bold()
				})
			})
			if ui.Sidebar(c, &a.page, func() {
				sidebarNavItem(c, pageSettings, settingsIcon, a.tr("设置"))
				sidebarNavItem(c, pageDevices, deviceIcon, a.tr("连接设备"))
				sidebarNavItem(c, pageHistory, historyIcon, a.tr("复制历史"))
			}).Grow(1).Changed() {
				a.confirm = false
			}
			ui.Column(c).Padding(12).Gap(8).Shrink(0).Children(func() {
				label := "未连接设备"
				link, up := a.engine.Link()
				if up {
					label = "已连接 · " + link.Route.Label()
				}
				if a.paused {
					label = "同步已暂停"
				}
				ui.Text(c, a.tr(label)).FontSize(12).Bold().SingleLine().ContentTransition(ui.ContentTransition{})
				if up {
					name := a.displayName(link.Name, link.ID)
					ui.Text(c, name).FontSize(12).MaxLines(3).Tooltip(name).ContentTransition(ui.ContentTransition{})
				}
				status := a.displayStatus()
				if up && status == "已通过"+link.Route.Label()+"连接 "+link.Name {
					status = ""
				}
				// Pairing copy is already on the devices page; don't repeat it under the sidebar.
				if status != "" && status != "未连接" && status != "已连接" && status != "已连接 "+link.Name && !(a.page == pageDevices && repeatsDevicesPage(status)) {
					shownStatus := a.tr(status)
					ui.Text(c, shownStatus).FontSize(11).TextColor(t.TextMuted).MaxLines(3).Tooltip(shownStatus).ContentTransition(ui.ContentTransition{})
				}
			})
		})
		ui.Column(c.Key("page-host")).Grow(1).FillHeight().Children(func() {
			// The drag strip is the page's own first row, not a sibling of it:
			// the page keeps filling the host, and the strip takes its room
			// from inside it, so the page's last row, the history actions,
			// still ends above the window's bottom edge.
			ui.Column(c.Key(a.page)).Fill().Transition(pageMotion).Children(func() {
				if bar.Height > 0 {
					ui.Box(c).Height(bar.Height).Shrink(0).DragWindow()
				}
				ui.Column(c).Grow(1).FillWidth().Padding(20, 28, 28, 28).Gap(20).Children(func() {
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
	})
	if a.page != pageHistory && a.pickPreview.parent == a.win {
		a.closePickPreview()
	}
}

func (a *app) settingsPage(c *ui.Context) {
	t := c.Theme()
	settingsGroup(c, a.tr("剪贴板同步"), func() {
		a.syncModeSettings(c)
		ui.Divider(c)
		if settingSwitch(c, &a.paused, a.tr("暂停同步"), "") {
			a.saveSettings()
		}
		ui.Divider(c)
		if settingSwitch(c, &a.textOnly, a.tr("只同步文本"), "") {
			a.saveSettings()
		}
		ui.Divider(c)
		a.ignoreNextCopySettings(c)
		ui.Divider(c)
		ui.Collapsible(c, a.tr("大小限制"), &a.showSyncLimits, func() {
			ui.Column(c).Gap(14).Padding(12, 0).Children(func() {
				ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(4).Children(func() {
						ui.Text(c, a.tr("文本大小上限")).Bold()
						ui.Text(c, a.tr("1–20 MB，超限不保存、不收发。")).FontSize(12).TextColor(t.TextMuted)
					})
					if ui.TextInput(c.Key("maxtextmb"), &a.maxTextText).Label(a.tr("文本大小上限，MB")).Width(56).Submitted() {
						a.saveTextLimit()
					}
					ui.Text(c, "MB").TextColor(t.TextMuted)
					if ui.Button(c, a.tr("应用")).Label(a.tr("应用文本上限")).Clicked() {
						a.saveTextLimit()
					}
				})
				if a.maxTextError != "" {
					ui.Text(c, a.tr(a.maxTextError)).FontSize(12).TextColor(t.Danger)
				}
				ui.Divider(c)
				ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(4).Children(func() {
						ui.Text(c, a.tr("图片大小上限")).Bold()
						ui.Text(c, a.tr("最多 20 MB，超限仅保留本机历史。")).FontSize(12).TextColor(t.TextMuted)
					})
					if ui.TextInput(c.Key("maxmb"), &a.maxImageText).Label(a.tr("图片大小上限，MB")).Width(56).Submitted() {
						a.saveSettings()
					}
					ui.Text(c, "MB").TextColor(t.TextMuted)
					if ui.Button(c, a.tr("应用")).Clicked() {
						a.saveSettings()
					}
				})
				ui.Divider(c)
				ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(4).Children(func() {
						ui.Text(c, a.tr("文件大小上限")).Bold()
						ui.Text(c, a.tr("默认 200 MB，整批内容合计，双方分别校验。")).FontSize(12).TextColor(t.TextMuted)
					})
					if ui.TextInput(c.Key("maxfilemb"), &a.maxFileText).Label(a.tr("文件大小上限，MB")).Width(88).Submitted() {
						a.saveFileLimit()
					}
					ui.Text(c, "MB").TextColor(t.TextMuted)
					if ui.Button(c, a.tr("应用")).Label(a.tr("应用文件上限")).Clicked() {
						a.saveFileLimit()
					}
				})
				if a.maxFileError != "" {
					ui.Text(c, a.tr(a.maxFileError)).FontSize(12).TextColor(t.Danger)
				}
			})
		})
	})
	ui.Row(c).Gap(16).AlignItems(ui.Stretch).Children(func() {
		settingsGroup(c, a.tr("复制并同步快捷键"), func() { a.copyShortcutSettings(c) }).Grow(1).Basis(0).MinWidth(0)
		settingsGroup(c, a.tr("快速历史快捷键"), func() { a.shortcutSettings(c) }).Grow(1).Basis(0).MinWidth(0)
	})
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		title := "菜单栏图标"
		if runtime.GOOS == "windows" {
			title = "托盘图标"
		}
		settingsGroup(c, a.tr(title), func() { a.menuBarIconSettings(c) })
	}
	settingsGroup(c, a.tr("历史存储"), func() { a.historyStorageSettings(c) })
	ui.Column(c.Key("开机启动")).Padding(16).Radius(12).Background(c.Theme().Surface).Children(func() {
		if settingSwitch(c, &a.openAtLogin, a.tr("开机启动"), "") {
			a.setOpenAtLogin(a.openAtLogin)
		}
	})
	if keepawake.Supported {
		ui.Column(c.Key("防止自动锁屏")).Padding(16).Radius(12).Background(c.Theme().Surface).Children(func() {
			a.keepAwakeSettings(c)
		})
	}
	settingsGroup(c, a.tr("语言"), func() { a.languageSettings(c) })
	settingsGroup(c, a.tr("跨网络连接"), func() { a.tailscaleSettings(c) })
	settingsGroup(c, a.tr("文件同步"), func() {
		ui.Collapsible(c, a.tr("接收文件夹"), &a.showReceiveFolder, func() {
			ui.Column(c).Padding(12, 0).Gap(10).Children(func() { a.receiveSettings(c) })
		})
	})
	settingsGroup(c, a.tr("本机与诊断"), func() {
		ui.Collapsible(c, a.tr("设备信息"), &a.showIdentity, func() {
			ui.Column(c).Padding(12, 0).Gap(10).Children(func() {
				kv(c, a.tr("名称"), a.name)
				kv(c, a.tr("设备 ID"), a.deviceID)
				ui.Text(c, a.tr("证书指纹")).FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, a.fingerprint).Selectable().FontSize(12).FontFeatures("tnum")
				if len(a.addrs) == 0 {
					kv(c, a.tr("地址"), a.tr("没有可用的局域网 IPv4"))
				}
				for _, addr := range a.addrs {
					kv(c, a.tr("地址"), addr)
				}
				for _, addr := range lan.IPv4() {
					if lan.TailscaleInterface(addr) {
						kv(c, "Tailscale IPv4", addr.IP.String())
					}
				}
			})
		})
		ui.Collapsible(c, a.tr("连接诊断"), &a.showDiagnostics, func() {
			ui.Column(c).Padding(12, 0).Children(func() {
				if ui.Button(c, a.tr("测试连接")).Clicked() {
					a.probe()
				}
			})
		})
	})
	settingsGroup(c, a.tr("关于与更新"), func() { a.updateSettings(c) })
}

func (a *app) historyPage(c *ui.Context) {
	a.pollHistoryPaste(c)
	var hoverID string
	var hoverBounds ui.Rect
	t := c.Theme()
	if a.historyUnavailable(c) {
		return
	}
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		if ui.TextInput(c.Key("query"), &a.query).Label(a.tr("搜索复制历史")).Placeholder(a.tr("搜索内容或来源")).Grow(1).Changed() {
			a.closePickPreview()
			a.histSel = -1
			a.histList.ScrollTo(0, ui.Start)
		}
		label := a.tr("清空未置顶")
		if a.confirmClear {
			label = a.tr("确认清空")
		}
		if press(ui.Button(c, label).Disabled(len(a.entries) == 0)).Clicked() {
			a.clearHistory()
		}
		if a.confirmClear && ui.Button(c, a.tr("取消")).Clicked() {
			a.confirmClear = false
		}
	})
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		if ui.Segmented(c, &a.historyKind, a.tr("全部"), a.tr("置顶"), a.tr("文本"), a.tr("图片"), a.tr("文件")).Label(a.tr("历史类型")).Changed() {
			a.closePickPreview()
			a.histSel = -1
			a.histList.ScrollTo(0, ui.Start)
		}
		ui.Spacer(c)
		ui.Text(c, a.trf("%d 条记录", "%d items", len(a.shown()))).FontSize(12).TextColor(t.TextMuted).
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
			emptyState(c, historyIcon, a.tr("还没有复制历史"), a.tr("复制文字、图片、文件或文件夹后会出现在这里"))
		case len(shown) == 0:
			message := ""
			if a.query != "" {
				message = a.historySearch.emptyMessage()
				if message == "没有匹配的记录。" {
					message = ""
				}
			}
			emptyState(c, searchIcon, a.tr("没有匹配的记录"), a.tr(message))
		default:
			if ui.List(c, &a.histList, len(shown), func(i int) {
				entry := shown[i]
				sel := i == a.histSel
				row := ui.Row(c).Padding(12, 10).Gap(12).AlignItems(ui.Center)
				var title ui.Element
				titleText := a.entryTitle(entry)
				row.Children(func() {
					a.historyThumb(c, entry, t, sel, 40)
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						title = a.historyMatchTitle(c, entry, sel)
						a.historyMatchDetail(c, entry, rowMetaColor(t, sel), 12, sel)
						ui.Row(c).Gap(6).Children(func() {
							if entry.Pinned {
								ui.Icon(c, pinIcon).FontSize(12).TextColor(rowMetaColor(t, sel)).Shrink(0)
							}
							a.historySource(c, entry, rowMetaColor(t, sel), 12)
						})
					})
					ui.Column(c).Gap(5).AlignItems(ui.End).Shrink(0).Children(func() {
						ui.Text(c, a.tr(relTime(entry.At))).FontSize(11).TextColor(rowMetaColor(t, sel)).SingleLine()
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
		if press(ui.PrimaryButton(c, a.tr("复制")).Disabled(a.histSel < 0 || a.historyPaste != nil || a.pasteWorker != nil || a.historyCleanupPending != nil)).Clicked() {
			a.pasteHistory()
		}
		ui.Toolbar(c, func() {
			label := "置顶"
			if entry, ok := a.selected(); ok && entry.Pinned {
				label = "取消置顶"
			}
			if press(ui.Button(c, a.tr(label)).Label(a.tr(label + "当前记录")).Disabled(a.histSel < 0 || a.historyCleanupPending != nil)).Clicked() {
				a.pinSelected()
			}
			if press(ui.Button(c, a.tr("删除")).Disabled(a.histSel < 0 || a.historyCleanupPending != nil)).Clicked() {
				a.deleteSelected()
			}
		}).Label(a.tr("记录操作"))
		ui.Spacer(c)
		if a.flash != "" {
			ui.Text(c, a.tr(a.flash)).FontSize(12).TextColor(t.TextMuted).SingleLine()
		}
	})
	a.updatePickPreview(c, a.win, hoverID, hoverBounds)
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
		a.engine.CancelDial(a.dialCancel)
		a.dialCancel = nil
	}
	a.dialAddr = ""
	a.dialTarget = nil
	a.dialProposed = lan.Endpoint{}
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
		if historyKindMatches(a.historyKind, entry) {
			shown = append(shown, entry)
		}
	}
	a.shownSource, a.shownEntries, a.shownKind = entries, shown, a.historyKind
	return shown
}

// The segmented control reads 全部, 置顶, 文本, 图片, 文件; 置顶 comes early
// because keeping a record is the more frequent action than one kind.
func historyKindMatches(kind int, entry history.Entry) bool {
	switch kind {
	case 1:
		return entry.Pinned
	case 2:
		return entry.Kind == string(clip.KindText)
	case 3:
		return entry.Kind == string(clip.KindImage)
	case 4:
		return entry.Kind == string(clip.KindFiles)
	default:
		return true
	}
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

func (a *app) toast(msg string) {
	a.pendingToast = msg
}

func kv(c *ui.Context, k, v string) {
	t := c.Theme()
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, k).MinWidth(48).TextColor(t.TextMuted).SingleLine()
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
			virtual = append(virtual, label+"  ·  "+virtualConnectionHint(addr))
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

func repeatsDevicesPage(status string) bool {
	switch status {
	case "核对两台电脑的校验码后确认配对", "已确认，等待另一台电脑确认":
		return true
	default:
		return false
	}
}
