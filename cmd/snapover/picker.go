package main

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/paste"
)

func pickerShortcuts() []string {
	if runtime.GOOS == "darwin" {
		return []string{"Alt+Cmd+V", "Cmd+Shift+V"}
	}
	return []string{"Alt+V"}
}

func (a *app) togglePicker() {
	a.openPicker(false)
}

func (a *app) openPicker(atCaret bool) {
	a.pasteSerial.Add(1)
	if a.picker != nil {
		a.picker.Close()
		return
	}
	if a.tray == nil {
		a.reveal(pageHistory)
		return
	}
	if t := paste.Capture(); t.Valid() {
		a.pasteTo = t
	}
	a.pickNeedsAccess = runtime.GOOS == "darwin" && !paste.Allowed()
	if a.hist != nil {
		a.refreshEntries()
	}
	a.pickQuery = ""
	a.pickFlash = ""
	a.pickSel = -1
	if len(a.filtered("")) > 0 {
		a.pickSel = 0
	}
	a.pickList.ScrollTo(0, ui.Start)
	if atCaret && (runtime.GOOS == "darwin" || runtime.GOOS == "windows") {
		paste.Prompt()
		bounds, scale := pickerScreenBounds(a.pasteTo)
		a.picker = mygo.NewFlyout(mygo.FlyoutOptions{
			Tray: a.tray, Width: bounds.Width, Height: bounds.Height,
			Hidden: true, Focusable: true, Shadow: true,
			Content: ui.View(a.pickerView),
		})
		if runtime.GOOS == "darwin" {
			// A nonactivating panel can take the keyboard over another app's
			// full-screen Space without activating Snapover and switching Spaces.
			a.picker.SetVisibleOnAllWorkspaces(true)
		}
		setPickerBounds(a.picker, bounds, scale)
	} else {
		a.picker = mygo.NewFlyout(mygo.FlyoutOptions{
			Tray:      a.tray,
			Placement: mygo.PlacementBottom,
			Gap:       4,
			Width:     380,
			Height:    440,
			Focusable: true,
			Shadow:    true,
			Popover:   runtime.GOOS == "darwin",
			Content:   ui.View(a.pickerView),
		})
	}
	a.picker.OnClosed(func() {
		if r := a.historyPaste; r != nil && r.picker == a.picker {
			a.cancelHistoryPaste()
		}
		a.pickerSearch.reset()
		a.closePickPreview()
		// ListState retains its window host; don't carry it into the next picker.
		a.pickList = ui.ListState{Key: a.pickList.Key, Label: a.pickList.Label, Selected: &a.pickSel}
		a.picker = nil
		a.pickBusy = false
	})
	a.picker.Show()
	a.picker.Focus()
}

func pickerBounds(anchor, area mygo.Rectangle) mygo.Rectangle {
	const gap = 4
	width, height := min(380, area.Width), min(440, area.Height)
	x, y := anchor.X, anchor.Y+anchor.Height+gap
	if y+height > area.Y+area.Height {
		y = anchor.Y - height - gap
	}
	return mygo.Rectangle{
		X:     max(area.X, min(x, area.X+area.Width-width)),
		Y:     max(area.Y, min(y, area.Y+area.Height-height)),
		Width: width, Height: height,
	}
}

func (a *app) rememberPasteTarget() {
	if t := paste.Capture(); t.Valid() {
		a.pasteTo = t
	}
}

func (a *app) startPasteTargetWatch(ctx context.Context) {
	stop, err := paste.WatchForeground(a.rememberPasteTarget)
	if err == nil {
		go func() {
			<-ctx.Done()
			mygo.RunOnMain(stop)
		}()
		return
	}
	if a.engine.Log != nil {
		a.engine.Log.Warn("前台窗口事件不可用，使用轮询兜底", "err", err)
	}
	go a.pollPasteTarget(ctx)
}

func (a *app) pollPasteTarget(ctx context.Context) {
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			mygo.RunOnMain(a.rememberPasteTarget)
		}
	}
}

func (a *app) closePicker() {
	a.pickerSearch.reset()
	if a.picker != nil {
		a.picker.Close()
	}
}

func (a *app) pickerView(c *ui.Context) {
	a.drainThumbnails()
	a.pollHistoryStorage()
	if c.Shortcut(0, ui.KeyEscape) {
		a.finishPick()
		return
	}
	a.pollHistoryPaste(c)
	var hoverID string
	var hoverBounds ui.Rect
	shown := a.filtered(a.pickQuery)
	if len(shown) > 0 {
		if c.Shortcut(0, ui.KeyDown) {
			a.movePick(1)
		}
		if c.Shortcut(0, ui.KeyUp) {
			a.movePick(-1)
		}
		if c.Shortcut(0, ui.KeyEnter) {
			a.pastePicked()
		}
	}
	t := c.Theme()
	c.Root().Background(ui.Transparent)
	box := ui.Column(c).Fill().Padding(14).Gap(10)
	switch {
	case c.Vibrancy():
	case runtime.GOOS == "windows":
		box.Background(t.Surface)
	default:
		box.Background(t.Surface).Radius(10).Border(1, t.Border)
	}
	box.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, historyIcon).FontSize(18).TextColor(t.TextMuted)
			ui.Text(c, "复制历史").FontSize(15).Bold().Grow(1)
			ui.Text(c, "↑↓ 选择 · ↵ 粘贴").FontSize(11).TextColor(t.TextMuted)
		})
		field := ui.TextInput(c.Key("pick-query"), &a.pickQuery).Label("搜索快速历史").AutoFocus().Placeholder("搜索内容或来源")
		if field.Shortcut(0, ui.KeyDown) {
			a.movePick(1)
		}
		if field.Shortcut(0, ui.KeyUp) {
			a.movePick(-1)
		}
		if field.Changed() {
			a.pickSel = 0
			if len(a.filtered(a.pickQuery)) == 0 {
				a.pickSel = -1
			}
			a.pickList.ScrollTo(0, ui.Start)
		}
		shown = a.filtered(a.pickQuery)
		if a.pickerSearch.pending != nil {
			c.After(50 * time.Millisecond)
		}
		if a.pickSel < 0 && len(shown) > 0 {
			a.pickSel = 0
		}
		if field.Submitted() {
			a.pastePicked()
		}
		switch {
		case len(a.entries) == 0:
			ui.Text(c, "还没有记录。先复制一段文字。").TextColor(t.TextMuted)
		case len(shown) == 0:
			ui.Text(c, a.pickerSearch.emptyMessage()).TextColor(t.TextMuted)
		default:
			list := ui.List(c, &a.pickList, len(shown), func(i int) {
				entry := shown[i]
				sel := i == a.pickSel
				row := ui.Row(c).Padding(9, 6).Gap(10).AlignItems(ui.Center)
				var title ui.Element
				titleText := entryTitle(entry)
				if row.DoubleClicked() {
					a.pickSel = i
					a.pastePicked()
				} else if row.Clicked() {
					a.pickSel = i
				}
				row.Children(func() {
					a.pickerThumb(c, entry, t, sel)
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						title = ui.Text(c, titleText).SingleLine()
						ui.Row(c).Gap(5).Children(func() {
							if entry.Pinned {
								ui.Icon(c, pinIcon).FontSize(11).TextColor(rowMetaColor(t, sel))
							}
							ui.Text(c, historySource(entry)+" · "+relTime(entry.At)).FontSize(11).TextColor(rowMetaColor(t, sel)).SingleLine()
						})
					})
				})
				if row.Hovered() && rowNeedsPreview(c, entry, titleText, title) {
					hoverID, hoverBounds = entry.ID, row.Bounds()
				}
			}).Grow(1)
			if list.Submitted() {
				a.pastePicked()
			}
		}
		if a.pickFlash != "" {
			ui.Text(c, a.pickFlash).FontSize(12).TextColor(t.TextMuted)
		}
		hint := "↑↓ 选择，回车或双击粘贴到光标处，Esc 关闭"
		if a.pickNeedsAccess {
			hint = "首次粘贴需要在辅助功能里允许 Snapover"
		}
		ui.Text(c, hint).FontSize(11).TextColor(t.TextMuted)
	})
	a.updatePickPreview(c, hoverID, hoverBounds)
}

func (a *app) movePick(delta int) {
	n := len(a.filtered(a.pickQuery))
	if n == 0 {
		return
	}
	a.pickSel += delta
	if a.pickSel < 0 {
		a.pickSel = n - 1
	} else if a.pickSel >= n {
		a.pickSel = 0
	}
	a.pickList.ScrollIntoView(a.pickSel)
}

func (a *app) pickerThumb(c *ui.Context, entry history.Entry, t *ui.Theme, selected bool) {
	a.historyThumb(c, entry, t, selected, 30)
}

func (a *app) pastePicked() {
	if a.pickBusy {
		return
	}
	shown := a.filtered(a.pickQuery)
	if a.pickSel < 0 || a.pickSel >= len(shown) {
		return
	}
	a.startHistoryPaste(shown[a.pickSel].ID, a.picker)
}

func (a *app) deliverPaste(target paste.Target) {
	serial := a.pasteSerial.Add(1)
	ctx := a.root
	if ctx == nil {
		ctx = context.Background()
	}
	wait := func(delay time.Duration) bool {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return a.pasteSerial.Load() == serial
		}
	}
	if !wait(80 * time.Millisecond) {
		return
	}
	var err error
	mygo.RunOnMain(func() {
		if ctx.Err() == nil && a.pasteSerial.Load() == serial {
			err = target.Activate()
		}
	})
	if err == nil && wait(60*time.Millisecond) {
		mygo.RunOnMain(func() {
			if ctx.Err() == nil && a.pasteSerial.Load() == serial {
				err = target.SendPaste()
			}
		})
	}
	if err == nil {
		return
	}
	a.notePaste(pasteFailure(err))
}

func (a *app) notePaste(msg string) {
	a.update(func() {
		a.status = msg
		a.flash = msg
		a.refreshTray()
	})
}

func pasteFailure(err error) string {
	switch {
	case errors.Is(err, paste.ErrNeedAccess):
		return "已复制。要直接粘贴，请在系统设置 → 隐私与安全性 → 辅助功能里允许 Snapover，然后再试一次"
	case errors.Is(err, paste.ErrNoTarget):
		return "已复制。刚才的窗口已经不在了，请再按一次粘贴"
	default:
		return "已复制，但没能把焦点还给刚才的窗口"
	}
}

func (a *app) finishPick() {
	p := a.picker
	if p == nil {
		return
	}
	// Closing is deferred until after the view; cancel a pending read now so
	// its completion cannot publish to the clipboard before OnClosed runs.
	if r := a.historyPaste; r != nil && r.picker == p {
		a.cancelHistoryPaste()
	}
	p.Update(func() {
		if a.picker == p {
			p.Close()
		}
	})
}
