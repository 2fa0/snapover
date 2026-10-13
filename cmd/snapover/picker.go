package main

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/paste"
)

func pickerShortcuts() []string {
	if runtime.GOOS == "darwin" {
		return []string{"Alt+Cmd+V", "Cmd+Shift+V"}
	}
	return []string{"Alt+V"}
}

func pickerHint() string {
	if runtime.GOOS == "darwin" {
		return "⌥⌘V 弹出快速历史，回车或双击粘贴到光标处。也能用 Cmd+Shift+V"
	}
	return "Alt+V 弹出快速历史，回车或双击粘贴到光标处"
}

func (a *app) registerPickerShortcut() {
	var failed []string
	for _, key := range pickerShortcuts() {
		if err := mygo.GlobalShortcut.Register(key, func() { a.openPicker(true) }); err != nil {
			failed = append(failed, key)
			if a.engine.Log != nil {
				a.engine.Log.Warn("全局快捷键没注册上", "key", key, "err", err)
			}
		}
	}
	if len(failed) == 0 {
		return
	}
	a.status = "快捷键被占用：" + strings.Join(failed, "、") + "。点菜单栏图标也能打开"
}

func (a *app) togglePicker() {
	a.openPicker(false)
}

func (a *app) openPicker(atCaret bool) {
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

func (a *app) watchPasteTarget(ctx context.Context) {
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
	var hoverID string
	var hoverBounds ui.Rect
	if c.Shortcut(0, ui.KeyEscape) {
		a.finishPick()
	}
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
	box := ui.Column(c).Fill().Padding(12).Gap(8)
	switch {
	case c.Vibrancy():
	case runtime.GOOS == "windows":
		box.Background(t.Surface)
	default:
		box.Background(t.Surface).Radius(10).Border(1, t.Border)
	}
	box.Children(func() {
		ui.Text(c, "复制历史").FontSize(15).Bold()
		field := ui.TextInput(c.Key("pick-query"), &a.pickQuery).AutoFocus().Placeholder("搜索，回车粘贴")
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
				row := ui.Row(c).Padding(6, 4).Gap(8).AlignItems(ui.Center)
				if row.Hovered() && entry.Kind == string(clip.KindImage) {
					hoverID, hoverBounds = entry.ID, row.Bounds()
				}
				if row.DoubleClicked() {
					a.pickSel = i
					a.pastePicked()
				} else if row.Clicked() {
					a.pickSel = i
				}
				row.Children(func() {
					a.pickerThumb(c, entry, t, sel)
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						title := entryTitle(entry)
						if entry.Pinned {
							title = "置顶  ·  " + title
						}
						ui.Text(c, title).SingleLine()
						ui.Text(c, entryMeta(entry)).FontSize(12).TextColor(rowMetaColor(t, sel)).SingleLine()
					})
				})
			}).Grow(1)
			if list.Submitted() {
				a.pastePicked()
			}
		}
		if a.pickFlash != "" {
			ui.Text(c, a.pickFlash).FontSize(12).TextColor(t.TextMuted)
		}
		hint := "↑↓ 选择，回车或双击粘贴到光标处，Esc 关闭"
		if runtime.GOOS == "darwin" && !paste.Allowed() {
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
	if entry.Kind != string(clip.KindImage) {
		return
	}
	if bmp := a.thumb(entry.ID); bmp != nil {
		ui.Image(c, bmp).Size(28, 28).Fit(ui.Cover).Radius(4)
		return
	}
	ui.Box(c).Size(28, 28).Radius(4).Background(rowThumbPlaceholder(t, selected))
}

func (a *app) pastePicked() {
	if a.pickBusy {
		return
	}
	shown := a.filtered(a.pickQuery)
	if a.pickSel < 0 || a.pickSel >= len(shown) {
		return
	}
	item, err := a.hist.Item(shown[a.pickSel].ID)
	if err != nil {
		a.pickFlash = "读历史失败"
		return
	}
	if err := a.board.Write(item); err != nil {
		a.pickFlash = "写回剪贴板失败"
		return
	}
	a.pickBusy = true
	target := a.pasteTo
	a.finishPick()
	if !target.Valid() {
		a.notePaste("已复制。没有记住刚才的窗口，请在目标处再按一次粘贴")
		return
	}
	go a.deliverPaste(target)
}

func (a *app) deliverPaste(target paste.Target) {
	time.Sleep(80 * time.Millisecond)
	var err error
	mygo.RunOnMain(func() { err = target.Paste() })
	if err == nil {
		return
	}
	a.notePaste(pasteFailure(err))
}

func (a *app) notePaste(msg string) {
	if a.win == nil {
		a.status = msg
		return
	}
	a.win.Update(func() {
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
	p.Update(func() {
		if a.picker == p {
			p.Close()
		}
	})
}
