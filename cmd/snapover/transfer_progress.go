package main

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	clipsync "github.com/2fa0/snapover/internal/sync"
)

const (
	largeTransferBytes  int64 = 20 << 20
	transferGlanceAfter       = 1500 * time.Millisecond
	transferDoneHold          = 2500 * time.Millisecond
)

const (
	transferClipboardSend    = "clipboard-send"
	transferClipboardReceive = "clipboard-receive"
	transferFileSend         = "file-send"
	transferFileReceive      = "file-receive"
)

const (
	phasePacking   = "packing"
	phaseSending   = "sending"
	phaseWaiting   = "waiting"
	phaseReceiving = "receiving"
	phaseUnpacking = "unpacking"
	phaseComplete  = "complete"
	phaseError     = "error"
)

type transferProgress struct {
	kind      string
	phase     string
	name      string
	message   string
	done      int64
	total     int64
	started   time.Time
	hideAt    time.Time
	gen       uint64
	glanced   bool
	dismissed bool
}

func (t transferProgress) bar() float64 {
	switch t.phase {
	case phasePacking, phaseUnpacking:
		return -1
	case phaseWaiting, phaseComplete:
		return 1
	case phaseError:
		if t.total > 0 {
			return float64(t.done) / float64(t.total)
		}
		return 1
	default:
		if t.total <= 0 {
			return -1
		}
		return float64(t.done) / float64(t.total)
	}
}

func (t transferProgress) label() string {
	name := t.name
	if name == "" {
		name = "文件"
	}
	switch t.phase {
	case phasePacking:
		return "正在打包 " + name + "…"
	case phaseSending:
		// The bar is the share; the label names what is moving, and detail()
		// gives the numbers beside it.
		return "正在同步 " + name + "…"
	case phaseWaiting:
		if t.kind == transferFileSend {
			return "传输完成，正在等待对方确认保存…"
		}
		return "对方处理中…"
	case phaseReceiving:
		return "正在接收 " + name + "…"
	case phaseUnpacking:
		return "正在整理 " + name + "…"
	case phaseComplete:
		switch t.kind {
		case transferClipboardSend:
			return "对方已可粘贴 " + name
		case transferFileSend:
			return "已发送 " + name
		case transferFileReceive:
			return "收到 " + name + "，已保存到接收文件夹"
		default:
			return "已可粘贴 " + name
		}
	case phaseError:
		if t.message != "" {
			return t.message
		}
		return "文件同步失败"
	default:
		return ""
	}
}

// tone tells a finished transfer from a failed one, which the bar shows.
func (t transferProgress) tone() progressTone {
	switch t.phase {
	case phaseComplete:
		return toneDone
	case phaseError:
		return toneFailed
	}
	return toneRunning
}

func (t transferProgress) detail() string {
	if t.total <= 0 || (t.phase != phaseSending && t.phase != phaseReceiving) {
		return ""
	}
	return formatSize(int(t.done)) + " / " + formatSize(int(t.total))
}

func (t transferProgress) glanceable(now time.Time) bool {
	if t.phase == "" {
		return false
	}
	if t.phase == phaseComplete || t.phase == phaseError {
		return t.glanced && !t.hideAt.IsZero() && now.Before(t.hideAt)
	}
	if t.total >= largeTransferBytes {
		return true
	}
	return !t.started.IsZero() && now.Sub(t.started) >= transferGlanceAfter
}

func transferKindOf(n clipsync.Notice, current string) string {
	if !n.FileClipboard {
		return transferFileReceive
	}
	switch n.FileState {
	case phasePacking, phaseSending, phaseWaiting:
		return transferClipboardSend
	case phaseReceiving, phaseUnpacking:
		return transferClipboardReceive
	case phaseComplete, phaseError:
		if current == transferClipboardSend || current == transferClipboardReceive {
			return current
		}
		return transferClipboardReceive
	default:
		return current
	}
}

func (a *app) beginOrUpdateTransfer(kind, phase, name string, done, total int64, message string) {
	same := a.transfer.kind == kind && a.transfer.phase != "" && a.transfer.phase != phaseComplete && a.transfer.phase != phaseError
	if !same {
		a.transfer = transferProgress{
			kind:    kind,
			name:    name,
			started: time.Now(),
			gen:     a.transfer.gen + 1,
		}
	}
	a.transfer.kind = kind
	a.transfer.phase = phase
	if name != "" {
		a.transfer.name = name
	}
	a.transfer.message = message
	a.transfer.done = done
	if total > 0 {
		a.transfer.total = total
	}
	switch phase {
	case phaseComplete, phaseError:
		now := time.Now()
		if a.transfer.glanced || a.transfer.total >= largeTransferBytes || (!a.transfer.started.IsZero() && now.Sub(a.transfer.started) >= transferGlanceAfter) {
			a.transfer.glanced = true
			a.transfer.hideAt = now.Add(transferDoneHold)
		}
	default:
		a.transfer.hideAt = time.Time{}
		if a.transfer.glanceable(time.Now()) {
			a.transfer.glanced = true
		}
	}
	a.syncTransferHUD()
	a.refreshTray()
	a.armTransferTimers()
}

func (a *app) armTransferTimers() {
	if a.tray == nil || a.transfer.phase == "" {
		return
	}
	gen := a.transfer.gen
	if a.transfer.phase == phaseComplete || a.transfer.phase == phaseError {
		if a.transfer.hideAt.IsZero() {
			return
		}
		wait := time.Until(a.transfer.hideAt)
		if wait < 0 {
			wait = 0
		}
		time.AfterFunc(wait, func() {
			mygo.RunOnMain(func() {
				if a.transfer.gen != gen || a.transfer.hideAt.IsZero() || time.Now().Before(a.transfer.hideAt) {
					return
				}
				a.clearTransfer()
				if a.win != nil {
					a.win.Invalidate()
				}
			})
		})
		return
	}
	if a.transfer.glanced {
		return
	}
	wait := transferGlanceAfter - time.Since(a.transfer.started)
	if wait <= 0 {
		return
	}
	time.AfterFunc(wait, func() {
		mygo.RunOnMain(func() {
			if a.transfer.gen != gen || a.transfer.glanced || a.transfer.phase == "" {
				return
			}
			if !a.transfer.glanceable(time.Now()) {
				return
			}
			a.transfer.glanced = true
			a.syncTransferHUD()
			a.refreshTray()
			if a.win != nil {
				a.win.Invalidate()
			}
		})
	})
}

func (a *app) clearTransfer() {
	if a.transfer.phase == "" && a.transferWin == nil {
		return
	}
	gen := a.transfer.gen
	a.transfer = transferProgress{gen: gen}
	a.syncTransferHUD()
	a.refreshTray()
}

func (a *app) applyFileTransfer(n clipsync.Notice) {
	switch n.FileState {
	case "cancelled":
		a.clearTransfer()
		return
	case phasePacking, phaseSending, phaseWaiting, phaseReceiving, phaseUnpacking, phaseComplete, phaseError:
	default:
		return
	}
	kind := transferKindOf(n, a.transfer.kind)
	if kind == "" {
		return
	}
	message := n.Message
	if n.FileState != phaseError {
		message = ""
	}
	a.beginOrUpdateTransfer(kind, n.FileState, n.FileName, n.FileDone, n.FileTotal, message)
}

func (a *app) applyClipboardTransfer(n clipsync.Notice) {
	switch n.ClipboardState {
	case "cancelled":
		a.clearTransfer()
	case "complete":
		if a.transfer.kind == transferClipboardSend && a.transfer.phase != "" && a.transfer.phase != phaseComplete && a.transfer.phase != phaseError {
			a.beginOrUpdateTransfer(transferClipboardSend, phaseComplete, a.transfer.name, a.transfer.done, a.transfer.total, "")
		}
	}
}

func (a *app) pumpTransfer(c *ui.Context) {
	now := time.Now()
	if a.transfer.phase == "" {
		return
	}
	glanced := a.transfer.glanceable(now)
	if a.transfer.phase == phaseComplete || a.transfer.phase == phaseError {
		if !glanced {
			a.clearTransfer()
			return
		}
		if d := time.Until(a.transfer.hideAt); d > 0 {
			c.After(d)
			return
		}
		a.clearTransfer()
		return
	}
	if glanced {
		if !a.transfer.glanced {
			a.transfer.glanced = true
			a.syncTransferHUD()
			a.refreshTray()
		}
		return
	}
	wait := transferGlanceAfter - now.Sub(a.transfer.started)
	if wait > 0 {
		c.After(wait)
	}
}

func (a *app) transferStatusText() (string, bool) {
	if !a.transfer.glanceable(time.Now()) {
		return "", false
	}
	label := a.transfer.label()
	return label, label != ""
}

func (a *app) sidebarTransfer(c *ui.Context, t *ui.Theme) {
	label, ok := a.transferStatusText()
	if !ok {
		return
	}
	ui.Text(c, a.tr(label)).FontSize(11).TextColor(t.TextMuted).MaxLines(3).Tooltip(a.tr(label)).ContentTransition(ui.ContentTransition{})
	progressBar(c, a.transfer.bar(), a.transfer.tone(), a.tr("文件同步进度"))
}

func (a *app) canShowTransferHUD() bool {
	return a.tray != nil && !a.quitting && a.fileWin == nil && (a.win == nil || !a.win.IsVisible())
}

func (a *app) syncTransferHUD() {
	show := a.canShowTransferHUD() && a.transfer.glanceable(time.Now()) && !a.transfer.dismissed
	if !show {
		if a.transferWin != nil {
			win := a.transferWin
			a.transferWin = nil
			win.Close()
		}
		return
	}
	if a.transferWin != nil {
		a.transferWin.Invalidate()
		if !a.transferWin.IsVisible() {
			a.transferWin.ShowInactive()
		}
		return
	}
	gen := a.transfer.gen
	win := mygo.NewWindow(a.placeHUDOnWorkingDisplay(mygo.WindowOptions{
		Title:             a.tr("文件同步 · Snapover"),
		Width:             360,
		Height:            132,
		MinWidth:          320,
		MinHeight:         120,
		MaxWidth:          480,
		MaxHeight:         200,
		DisableMaximize:   true,
		DisableFullScreen: true,
		SkipTaskbar:       true,
		AlwaysOnTop:       true,
		Hidden:            true,
		Content:           ui.View(a.transferView),
	}))
	a.transferWin = win
	win.OnClosed(func() {
		if a.transferWin == win {
			a.transferWin = nil
		}
		if a.transfer.gen == gen && a.transfer.phase != "" && a.transfer.phase != phaseComplete && a.transfer.phase != phaseError {
			a.transfer.dismissed = true
		}
	})
	win.ShowInactive()
}

func (a *app) transferView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(16).Gap(10).Background(t.Background).Children(func() {
		ui.Text(c, a.tr(a.transfer.label())).Bold().MaxLines(2)
		progressBar(c, a.transfer.bar(), a.transfer.tone(), a.tr("文件同步进度"))
		if detail := a.transfer.detail(); detail != "" {
			ui.Text(c, detail).FontSize(12).TextColor(t.TextMuted)
		}
	})
}
