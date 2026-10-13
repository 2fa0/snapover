package main

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/paste"
)

type pasteResult struct {
	item clip.Item
	err  error
}

type pasteRequest struct {
	id     string
	picker *mygo.Window
	target paste.Target
	result chan pasteResult
}

func (a *app) startHistoryPaste(id string, picker *mygo.Window) {
	if a.historyPaste != nil {
		return
	}
	r := &pasteRequest{id: id, picker: picker, target: a.pasteTo, result: make(chan pasteResult, 1)}
	a.historyPaste = r
	if picker != nil {
		a.pickBusy = true
		a.pickFlash = "正在准备粘贴…"
	} else {
		a.flash = "正在读取历史…"
	}
	store, host := a.hist, a.win
	if picker != nil {
		host = picker
	}
	go func() {
		item, err := store.PasteItem(id)
		r.result <- pasteResult{item, err}
		if host != nil {
			host.Update(func() { a.completeHistoryPaste(r) })
		}
	}()
}

func (a *app) pollHistoryPaste(c *ui.Context) {
	if r := a.historyPaste; r != nil {
		a.completeHistoryPaste(r)
		if a.historyPaste != nil && a.win == nil && a.picker == nil {
			c.After(50 * time.Millisecond)
		}
	}
}

func (a *app) completeHistoryPaste(r *pasteRequest) {
	if a.historyPaste != r {
		return
	}
	var result pasteResult
	select {
	case result = <-r.result:
	default:
		return
	}
	defer func() {
		if a.historyPaste == r {
			a.historyPaste = nil
			a.pickBusy = false
		}
	}()
	if r.picker != nil && a.picker != r.picker {
		return
	}
	// A deletion or clear while the disk read was running cancels this action.
	found := false
	for _, entry := range a.entries {
		if entry.ID == r.id {
			found = true
			break
		}
	}
	message := "读历史失败"
	if !found {
		message = "记录已删除，请重新选择"
	} else if result.err == nil {
		if err := a.board.Write(result.item); err == nil {
			if r.picker == nil {
				a.flash = "已写回剪贴板"
				return
			}
			a.finishPick()
			if !r.target.Valid() {
				a.notePaste("已复制。没有记住刚才的窗口，请在目标处再按一次粘贴")
				return
			}
			go a.deliverPaste(r.target)
			return
		}
		message = "写回剪贴板失败"
	}
	if r.picker != nil {
		a.pickFlash = message
	} else {
		a.flash = message
	}
}
