package main

import (
	"context"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/paste"
)

type pasteResult struct {
	item     clip.Item
	err      error
	prepared clip.PreparedWrite
}

type pasteRequest struct {
	id     string
	picker *mygo.Window
	target paste.Target
	result chan pasteResult
	cancel context.CancelFunc
	ctx    context.Context
}

func (a *app) cancelHistoryPaste() {
	if r := a.historyPaste; r != nil && r.cancel != nil {
		r.cancel()
	}
	a.historyPaste = nil
	a.queuedPaste = nil
	a.pickBusy = false
}

func (a *app) startHistoryPaste(id string, picker *mygo.Window) {
	a.pollHistoryStorage()
	if a.pasteWorker != nil {
		a.completeHistoryPaste(a.pasteWorker)
	}
	if a.historyCleanupPending != nil {
		if picker != nil {
			a.pickFlash = "正在清理历史，请稍后再试"
		} else {
			a.flash = "正在清理历史，请稍后再试"
		}
		return
	}
	if a.historyPaste != nil {
		return
	}
	parent := a.root
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	r := &pasteRequest{id: id, picker: picker, target: a.pasteTo, result: make(chan pasteResult, 1)}
	r.cancel, r.ctx = cancel, ctx
	a.historyPaste = r
	if picker != nil {
		a.pickBusy = true
		a.pickFlash = "正在准备粘贴…"
	} else {
		a.flash = "正在读取历史…"
	}
	if a.pasteWorker != nil {
		// Keep only the newest intent, without loading another large image
		// while a canceled worker is still returning from disk/native work.
		a.queuedPaste = r
		return
	}
	a.prepareHistoryPaste(r)
}

func (a *app) prepareHistoryPaste(r *pasteRequest) {
	a.pasteWorker = r
	store, board, ctx := a.hist, a.board, r.ctx
	go func() {
		item, err := store.PasteItem(r.id)
		var prepared clip.PreparedWrite
		if err == nil && ctx.Err() == nil {
			if b, ok := board.(interface {
				PrepareWrite(clip.Item) (clip.PreparedWrite, error)
			}); ok {
				prepared, err = b.PrepareWrite(item)
			}
		}
		if ctx.Err() != nil {
			prepared.Close()
			prepared, item, err = clip.PreparedWrite{}, clip.Item{}, ctx.Err()
		}
		r.result <- pasteResult{item: item, err: err, prepared: prepared}
		if a.root != nil {
			a.update(func() { a.completeHistoryPaste(r) })
		}
	}()
}

func (a *app) pollHistoryPaste(c *ui.Context) {
	if r := a.pasteWorker; r != nil {
		a.completeHistoryPaste(r)
	}
	if r := a.historyPaste; r != nil {
		a.completeHistoryPaste(r)
		if c != nil && a.historyPaste != nil && a.win == nil && a.picker == nil {
			c.After(50 * time.Millisecond)
		}
	}
}

func (a *app) completeHistoryPaste(r *pasteRequest) {
	var result pasteResult
	select {
	case result = <-r.result:
	default:
		return
	}
	defer func() {
		result.prepared.Close()
		if next := a.queuedPaste; next != nil && a.pasteWorker == nil {
			a.queuedPaste = nil
			if a.historyPaste == next {
				a.prepareHistoryPaste(next)
			}
		}
	}()
	if r.cancel != nil {
		defer r.cancel()
	}
	if a.pasteWorker == r {
		a.pasteWorker = nil
	}
	if a.historyPaste != r {
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
		publish := result.prepared.Publish
		if publish == nil {
			publish = func() error { return a.board.Write(result.item) }
		}
		if err := publish(); err == nil {
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
