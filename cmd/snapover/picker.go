package main

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
	"time"

	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/paste"
	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
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

func (a *app) openPicker(atPointer bool) {
	a.pasteSerial.Add(1)
	if a.picker != nil {
		a.picker.Close()
		return
	}
	if a.tray == nil {
		a.reveal(pageHistory)
		return
	}
	a.closePickPreview()
	if t := paste.Capture(); t.Valid() {
		a.pasteTo = t
	}
	a.pickNeedsAccess = runtime.GOOS == "darwin" && !paste.Allowed()
	if a.hist != nil {
		a.refreshEntries()
	}
	a.pickQuery = ""
	a.pickFlash = ""
	a.pickRestore = false
	a.pickMutationPending = false
	a.pickSel = -1
	if len(a.filtered("")) > 0 {
		a.pickSel = 0
	}
	a.pickList.ScrollTo(0, ui.Start)
	width, height := a.pickerSize()
	a.pickSizeW, a.pickSizeH = width, height
	a.pickDragW, a.pickDragH = float32(width), float32(height)
	a.pickResizing = false
	// Shortcut panels stay on the working display. They follow the pointer
	// only when it is on that display; otherwise they sit by the caret or
	// in the front window so a leftover pointer on another screen is ignored.
	if atPointer && (runtime.GOOS == "darwin" || runtime.GOOS == "windows") {
		paste.Prompt()
		bounds, scale := pickerScreenBounds(a.pasteTo, width, height)
		a.pickPlaced, a.pickRect, a.pickScale = true, bounds, scale
		// The flyout cannot grow past the work area it opens in.
		a.pickArea = pickerAreaAt(bounds)
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
		// The platform places this one next to the tray icon; the first
		// frame reads back where it landed.
		a.pickArea = mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea
		a.pickPlaced = false
		a.pickRect, a.pickScale = mygo.Rectangle{}, 1
		opts := mygo.FlyoutOptions{
			Tray:      a.tray,
			Placement: mygo.PlacementBottom,
			Gap:       4,
			Width:     width,
			Height:    height,
			Focusable: true,
			Shadow:    true,
			Content:   ui.View(a.pickerView),
		}
		if runtime.GOOS == "darwin" {
			// A status-item NSPopover does not close on outside clicks in
			// an accessory app; a focusable flyout panel does.
			opts.Vibrancy = mygo.VibrancyPopover
			opts.CornerRadius = 10
		}
		a.picker = mygo.NewFlyout(opts)
	}
	a.picker.OnClosed(func() {
		if r := a.historyPaste; r != nil && r.picker == a.picker {
			a.cancelHistoryPaste()
		}
		a.pickerSearch.reset()
		if a.pickPreview.parent == a.picker {
			a.closePickPreview()
		}
		// ListState retains its window host; don't carry it into the next picker.
		a.pickList = ui.ListState{Key: a.pickList.Key, Label: a.pickList.Label, Selected: &a.pickSel}
		a.picker = nil
		a.pickBusy = false
		a.pickRestore = false
		a.pickMutationPending = false
		if a.editNotePicker {
			a.cancelEditNote()
		}
	})
	a.picker.Show()
	a.picker.Focus()
	// A tray flyout is placed by the platform: read where it landed so a drag
	// or a resize moves it from there. The pointer flyout is already recorded.
	a.syncPickerPlacement()
}

func pickerBounds(anchor, area mygo.Rectangle, width, height int) mygo.Rectangle {
	const gap = 4
	width, height = min(width, area.Width), min(height, area.Height)
	// Center on the pointer so a large panel stays around the hand instead
	// of hanging off to the right. Keep the pointer just above the panel so
	// it does not rest on a history row.
	x := anchor.X + max(anchor.Width, 0)/2 - width/2
	y := anchor.Y + anchor.Height + gap
	if y+height > area.Y+area.Height {
		y = anchor.Y - height - gap
	}
	return mygo.Rectangle{
		X:     max(area.X, min(x, area.X+area.Width-width)),
		Y:     max(area.Y, min(y, area.Y+area.Height-height)),
		Width: width, Height: height,
	}
}

// A caret is only an insertion point when it is a short, narrow rectangle.
// Accessibility often returns the whole editor instead.
func trustworthyPickerCaret(r mygo.Rectangle) bool {
	return r.Height > 0 && r.Height <= 80 && r.Width >= 0 && r.Width <= 480
}

func pickerCenteredIn(host, area mygo.Rectangle, width, height int) mygo.Rectangle {
	width, height = min(width, area.Width), min(height, area.Height)
	x := host.X + (host.Width-width)/2
	y := host.Y + (host.Height-height)/2
	return mygo.Rectangle{
		X:     max(area.X, min(x, area.X+area.Width-width)),
		Y:     max(area.Y, min(y, area.Y+area.Height-height)),
		Width: width, Height: height,
	}
}

type pickerPlaceInput struct {
	width, height int
	mouse         mygo.Rectangle
	mouseArea     mygo.Rectangle
	mouseOnWork   bool
	caret         mygo.Rectangle
	hasCaret      bool
	window        mygo.Rectangle
	hasWindow     bool
	workArea      mygo.Rectangle
}

// placeShortcutPicker keeps the panel on the working display. The pointer
// wins only when it is already there.
func placeShortcutPicker(p pickerPlaceInput) mygo.Rectangle {
	work := p.workArea
	if work.Width <= 0 || work.Height <= 0 {
		work = p.mouseArea
	}
	if p.mouseOnWork {
		return pickerBounds(p.mouse, p.mouseArea, p.width, p.height)
	}
	if p.hasCaret && trustworthyPickerCaret(p.caret) {
		return pickerBounds(p.caret, work, p.width, p.height)
	}
	if p.hasWindow {
		return pickerCenteredIn(p.window, work, p.width, p.height)
	}
	return pickerBounds(p.mouse, p.mouseArea, p.width, p.height)
}

// pickerSize is the size the picker opens at: the one the pointer left it at
// last time, or the default before the first drag.
func (a *app) pickerSize() (int, int) {
	if a.cfg == nil {
		return settings.DefaultPickerWidth, settings.DefaultPickerHeight
	}
	return a.cfg.PickerSize()
}

// The picker has no frame, so the platform draws no resize border: this grip
// along the right and bottom edges and the corner between them stands in for
// one. Dragging grows the window in the direction of the pointer, which keeps
// the pointer inside it.
const pickerResizeGrip = 6

func (a *app) pickerResizeHandles(c *ui.Context) {
	width, height := c.Size()
	if width < pickerResizeGrip*2 || height < pickerResizeGrip*2 {
		return
	}
	w, h, grip := float32(width), float32(height), float32(pickerResizeGrip)
	right, bottom := w-grip, h-grip
	ui.Overlay(c, func() {
		a.dragPickerSize(ui.Column(c).Absolute().Left(right).Top(0).Width(grip).Height(h).Cursor(ui.CursorResizeEW), true, false)
		a.dragPickerSize(ui.Column(c).Absolute().Left(0).Top(bottom).Width(w).Height(grip).Cursor(ui.CursorResizeNS), false, true)
		a.dragPickerSize(ui.Column(c).Absolute().Left(right).Top(bottom).Width(grip).Height(grip).Cursor(ui.CursorResizeNWSE), true, true)
	})
}

// dragPickerSize sizes the open picker from the handle the pointer holds.
// Dragged reports the movement of one frame, so the size the drag started at
// accumulates the frames instead of the window chasing its own result.
func (a *app) dragPickerSize(handle ui.Element, horizontal, vertical bool) {
	dx, dy, held := handle.Dragged()
	if !held {
		if a.pickResizing {
			a.pickResizing = false
			a.savePickerSize()
		}
		return
	}
	if !a.pickResizing {
		a.pickResizing = true
		a.pickDragW, a.pickDragH = float32(a.pickSizeW), float32(a.pickSizeH)
	}
	width, height := a.pickDragW, a.pickDragH
	if horizontal {
		width += dx
	}
	if vertical {
		height += dy
	}
	a.pickDragW, a.pickDragH = a.clampPickerSize(width, height)
	a.applyPickerSize(int(math.Round(float64(a.pickDragW))), int(math.Round(float64(a.pickDragH))))
}

// clampPickerSize keeps the window usable, and inside the work area it is on.
func (a *app) clampPickerSize(width, height float32) (float32, float32) {
	minW, minH := float32(settings.MinPickerWidth), float32(settings.MinPickerHeight)
	maxW, maxH := float32(settings.MaxPickerWidth), float32(settings.MaxPickerHeight)
	if a.pickArea.Width > 0 {
		maxW = min(maxW, float32(a.pickArea.Width))
	}
	if a.pickArea.Height > 0 {
		maxH = min(maxH, float32(a.pickArea.Height))
	}
	return min(max(width, minW), max(minW, maxW)), min(max(height, minH), max(minH, maxH))
}

// syncPickerPlacement records where the window is now. The tray flyout is
// placed by the platform, so its record is read back once it shows, on the
// main thread: reading it while building a frame would block a test's frame
// loop, which no window answers.
func (a *app) syncPickerPlacement() {
	if a.picker == nil || a.pickPlaced {
		return
	}
	bounds := a.picker.Bounds()
	if bounds.Width <= 0 || bounds.Height <= 0 {
		return
	}
	a.pickPlaced, a.pickRect = true, bounds
	a.pickSizeW, a.pickSizeH = bounds.Width, bounds.Height
	a.pickArea = pickerAreaAt(bounds)
	a.pickScale = float64(mygo.Screen.DisplayNearestPoint(mygo.Point{X: bounds.X, Y: bounds.Y}).ScaleFactor)
}

// pickerAreaAt is the work area of the display the picker is on.
func pickerAreaAt(bounds mygo.Rectangle) mygo.Rectangle {
	return mygo.Screen.DisplayNearestPoint(mygo.Point{X: bounds.X, Y: bounds.Y}).WorkArea
}

// pickerCursor is where the pointer is on the screen, in the DIPs a window's
// bounds are in. A test moves it to drive a drag.
var pickerCursor = func() mygo.Point { return mygo.Screen.CursorScreenPoint() }

var pickerDisplayAt = func(p mygo.Point) (mygo.Rectangle, float64) {
	d := mygo.Screen.DisplayNearestPoint(p)
	return d.WorkArea, d.ScaleFactor
}

// dragPickerMove moves the window with the pointer. The picker has no title
// bar, so its padding and the seams beside the search field do the job: they
// take the press themselves, while a row, the field and the grips above them
// keep theirs.
//
// Where the window goes comes from where the pointer is on the screen, never
// from the movements the press reports. A window that follows the pointer
// changes the frame those movements are measured in: added up, each frame
// counts the movement of a window a frame behind, then hands back what it put
// there, and the window shakes against the pointer instead of following it.
func (a *app) dragPickerMove(handle ui.Element) {
	if _, _, held := handle.Dragged(); !held {
		a.pickMoving = false
		return
	}
	if !a.pickPlaced {
		return
	}
	at := pickerCursor()
	if !a.pickMoving {
		a.pickMoving = true
		a.pickGrab = at
		a.pickX, a.pickY = a.pickRect.X, a.pickRect.Y
		// A preview still open from the row the pointer left would follow the
		// window a frame behind it.
		a.closePickPreview()
	}
	a.followPickerDisplay(at)
	x, y := a.pickX+at.X-a.pickGrab.X, a.pickY+at.Y-a.pickGrab.Y
	if a.pickArea.Width > 0 && a.pickArea.Height > 0 {
		// Stay on one display. Crossing into another work area moves the
		// whole window there; stopping at an edge holds no offset.
		x = min(max(x, a.pickArea.X), max(a.pickArea.X, a.pickArea.X+a.pickArea.Width-a.pickSizeW))
		y = min(max(y, a.pickArea.Y), max(a.pickArea.Y, a.pickArea.Y+a.pickArea.Height-a.pickSizeH))
	}
	rect := mygo.Rectangle{X: x, Y: y, Width: a.pickSizeW, Height: a.pickSizeH}
	if rect == a.pickRect {
		return
	}
	a.pickRect = rect
	if a.picker != nil {
		setPickerBounds(a.picker, rect, a.pickScale)
	}
}

func (a *app) followPickerDisplay(at mygo.Point) {
	area, scale := pickerDisplayAt(at)
	if area.Width <= 0 || area.Height <= 0 {
		return
	}
	a.pickArea = area
	if scale > 0 {
		a.pickScale = scale
	}
	if a.pickSizeW > area.Width {
		a.pickSizeW = area.Width
	}
	if a.pickSizeH > area.Height {
		a.pickSizeH = area.Height
	}
}

func (a *app) applyPickerSize(width, height int) {
	if width == a.pickSizeW && height == a.pickSizeH {
		return
	}
	a.pickSizeW, a.pickSizeH = width, height
	rect := a.pickRect
	rect.Width, rect.Height = width, height
	// Growing past a screen edge slides the window back inside instead of
	// pushing the edge the pointer follows out of reach.
	if a.pickArea.Width > 0 && a.pickArea.Height > 0 {
		rect.X = max(a.pickArea.X, min(rect.X, a.pickArea.X+a.pickArea.Width-width))
		rect.Y = max(a.pickArea.Y, min(rect.Y, a.pickArea.Y+a.pickArea.Height-height))
	}
	if !a.pickPlaced || rect == a.pickRect {
		return
	}
	a.pickRect = rect
	if a.picker != nil {
		setPickerBounds(a.picker, rect, a.pickScale)
	}
}

// savePickerSize remembers the size the drag left, so opening the picker again
// keeps it.
func (a *app) savePickerSize() {
	if a.cfg == nil || a.pickSizeW <= 0 || a.pickSizeH <= 0 {
		return
	}
	if a.cfg.Values.PickerWidth == a.pickSizeW && a.cfg.Values.PickerHeight == a.pickSizeH {
		return
	}
	previousWidth, previousHeight := a.cfg.Values.PickerWidth, a.cfg.Values.PickerHeight
	a.cfg.Values.PickerWidth, a.cfg.Values.PickerHeight = a.pickSizeW, a.pickSizeH
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values.PickerWidth, a.cfg.Values.PickerHeight = previousWidth, previousHeight
		a.status = "快速历史窗口大小没能保存，请检查数据目录权限后重试"
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
	editingNote := a.editNoteID != "" && a.editNotePicker
	if c.Shortcut(0, ui.KeyEscape) {
		if editingNote {
			a.cancelEditNote()
			return
		}
		a.finishPick()
		return
	}
	a.pollHistoryPaste(c)
	for _, shortcut := range []struct {
		mods ui.Modifiers
		key  ui.Key
	}{{ui.Alt, ui.KeyEnter}, {ui.Cmd | ui.Shift, ui.KeyP}, {ui.Cmd | ui.Shift, ui.KeyBackspace}} {
		if c.Shortcut(shortcut.mods, shortcut.key) {
			a.pickerActionKey(shortcut.mods, shortcut.key)
		}
	}
	for key := ui.Key1; key <= ui.Key9; key++ {
		if c.Shortcut(ui.Cmd, key) {
			a.pickerActionKey(ui.Cmd, key)
		}
	}
	if a.historyCleanupPending != nil {
		c.After(50 * time.Millisecond)
	}
	var hoverID string
	var hoverBounds ui.Rect
	shown := a.filtered(a.pickQuery)
	if len(shown) > 0 && !editingNote {
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
	// The window has no title bar: its padding and the seams between the
	// search field and the list move it, as a title bar would.
	box := ui.Column(c).Fill().Padding(14).Gap(10).Cursor(ui.CursorGrab)
	switch {
	case c.Vibrancy():
	case runtime.GOOS == "windows":
		box.Background(t.Surface)
	default:
		box.Background(t.Surface).Radius(10).Border(1, t.Border)
	}
	box.Children(func() {
		var searchField ui.Element
		// The count sits beside the field, where it costs no room until a
		// query takes the placeholder's place.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			field, cleared := a.searchBar(c.Key("pick-query"), &a.pickQuery, a.tr("搜索快速历史"), a.pickerSearch.pending != nil)
			searchField = field
			if !editingNote {
				field = field.AutoFocus()
			}
			field = field.OnTextKey(func(k ui.TextKey) bool {
				if k.Mods == ui.Alt && k.Key == ui.KeyEnter || k.Mods == ui.Cmd|ui.Shift && (k.Key == ui.KeyP || k.Key == ui.KeyBackspace) || k.Mods == ui.Cmd && k.Key >= ui.Key1 && k.Key <= ui.Key9 {
					a.pickQuery = k.Text()
					return a.pickerActionKey(k.Mods, k.Key)
				}
				return false
			})
			if field.Shortcut(0, ui.KeyDown) {
				a.movePick(1)
			}
			if field.Shortcut(0, ui.KeyUp) {
				a.movePick(-1)
			}
			if field.Changed() || cleared {
				// A newer search is a new selection intent. A pending mutation
				// must not restore the old query's selected record afterwards.
				a.pickRestore = false
				if cleared {
					// Clearing must also override the list's remembered row key.
					a.pickRestore, a.pickRestoreID, a.pickRestoreIndex = true, "", 0
				}
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
			if field.Submitted() && !editingNote {
				a.pastePicked()
			}
			if strings.TrimSpace(a.pickQuery) != "" {
				ui.Text(c, a.tr(a.pickerSearch.countLabel(len(shown)))).FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(0)
			}
		})
		a.noteEditorRow(c, true)
		switch {
		case len(a.entries) == 0:
			emptyState(c, historyIcon, a.tr("还没有记录"), a.tr("复制文字、图片、文件或文件夹后会出现在这里"), nil)
		case len(shown) == 0:
			emptyState(c, searchIcon, a.tr(a.pickerSearch.emptyMessage()), "", func() {
				if a.pickQuery != "" && ui.Button(c, a.tr("清空搜索")).Label(a.tr("清空搜索并查看全部记录")).Clicked() {
					a.pickQuery = ""
					a.pickRestore, a.pickRestoreID, a.pickRestoreIndex = true, "", 0
					a.pickSel = 0
					a.pickList.ScrollTo(0, ui.Start)
					searchField.Focus()
					c.Invalidate()
				}
			})
		default:
			list := ui.List(c, &a.pickList, len(shown), func(i int) {
				entry := shown[i]
				sel := i == a.pickSel
				row := ui.Row(c.Key("pick-row:"+entry.ID)).Padding(9, 6).Gap(10).AlignItems(ui.Center)
				row.ContextMenu(func(m *ui.Menu) {
					a.pickSel = i
					a.pickerEntryMenu(m, entry)
				})
				var title ui.Element
				titleText := a.entryTitle(entry)
				row.Children(func() {
					a.pickerThumb(c, entry, t, sel)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						title = a.historyMatchTitle(c, entry, sel)
						a.historyMatchDetail(c, entry, rowMetaColor(t, sel), 11, sel)
						ui.Row(c).Gap(5).Children(func() {
							if entry.Pinned {
								ui.Icon(c, pinIcon).FontSize(11).TextColor(rowMetaColor(t, sel)).Shrink(0)
							}
							a.historySource(c, entry, rowMetaColor(t, sel), 11)
							ui.Text(c, a.tr(relTime(entry.At))).FontSize(11).TextColor(rowMetaColor(t, sel)).SingleLine().Shrink(0)
						})
					})
					if entry.Pinned && entry.PinKey > 0 && entry.PinKey <= 9 {
						hotkeyCaps(c, pinShortcutLabel(entry.PinKey)).
							Tooltip(a.tr("在快速历史内直接粘贴此条记录"))
					}
				})
				if row.Hovered() && rowNeedsPreview(c, entry, titleText, title) {
					hoverID, hoverBounds = entry.ID, row.Bounds()
				}
			}).Grow(1)
			if list.Submitted() {
				a.pastePicked()
			}
		}
		// List first reconciles its old selected key. Restore afterwards so
		// deleting that key cannot overwrite the adjacent row we selected.
		if a.pickRestore {
			a.restorePickSelection(shown)
			if !a.pickRestore {
				c.After(time.Millisecond)
			}
		}
		if a.pickFlash != "" {
			ui.Text(c, a.tr(a.pickFlash)).FontSize(12).TextColor(t.TextMuted)
		}
		// Only a state that changes what pasting can do earns a line here; the
		// everyday actions and their shortcuts live in the context menu.
		if a.pickNeedsAccess {
			ui.Text(c, a.tr("首次粘贴需要在辅助功能里允许 Snapover")).FontSize(11).TextColor(t.TextMuted)
		}
	})
	a.dragPickerMove(box)
	width, _ := c.Size()
	hoverBounds.X, hoverBounds.W = 0, width
	a.pickerResizeHandles(c)
	a.updatePickPreview(c, a.picker, hoverID, hoverBounds)
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
