package main

import (
	"context"
	"errors"
	"time"

	"github.com/2fa0/snapover/internal/paste"
	clipsync "github.com/2fa0/snapover/internal/sync"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *app) startCopySync() {
	if a.copyBusy {
		return
	}
	target := paste.Capture()
	if !target.Valid() {
		a.copySyncFailure(errors.New("请先在其他应用中选中要复制的内容"))
		return
	}
	a.copyBusy = true
	ctx := a.root
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		err := a.copySelection(ctx, target)
		a.update(func() {
			a.copyBusy = false
			if err != nil && ctx.Err() == nil {
				a.copySyncFailure(err)
			}
		})
	}()
}

func (a *app) copySelection(ctx context.Context, target paste.Target) error {
	// Wait off the UI thread so holding the hotkey cannot turn Ctrl+C into
	// Ctrl+Alt+C (or recursively trigger the custom shortcut).
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	release := time.NewTimer(2 * time.Second)
	defer release.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release.C:
			return errors.New("请松开快捷键后再试")
		case <-tick.C:
		}
		var ready bool
		var result <-chan error
		var claimed <-chan struct{}
		var cancel func() bool
		var err error
		mygo.RunOnMain(func() {
			if ctx.Err() != nil {
				err = ctx.Err()
				return
			}
			if paste.Capture() != target {
				err = errors.New("当前应用已切换，请重新复制并同步")
				return
			}
			if paste.ModifiersDown() {
				return
			}
			ready = true
			var seq uint64
			seq, err = a.board.Sequence()
			if err != nil {
				return
			}
			result, claimed, cancel, err = a.engine.CopyNext(seq)
			if err == nil {
				err = target.SendCopy()
			}
		})
		if cancel != nil {
			defer cancel()
		}
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		timeout := time.NewTimer(clipsync.CopyTimeout)
		defer timeout.Stop()
		observed := false
		for !observed {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-result:
				return err
			case <-claimed:
				observed = true
			case <-timeout.C:
				if cancel() {
					return errors.New("未复制到新内容，请选中文本、图片或文件后再试")
				}
				observed = true
			case <-tick.C:
				var changed bool
				mygo.RunOnMain(func() { changed = paste.Capture() != target })
				if changed && cancel() {
					return errors.New("当前应用已切换，请重新复制并同步")
				}
			}
		}
		processing := time.NewTimer(clipsync.CopyProcessTimeout)
		defer processing.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-result:
				return err
			case <-processing.C:
				cancel()
				return errors.New("复制处理超时，已保留在本机历史，请稍后查看")
			case <-tick.C:
				var changed bool
				mygo.RunOnMain(func() { changed = paste.Capture() != target })
				if changed && cancel() {
					return errors.New("当前应用已切换，请重新复制并同步")
				}
			}
		}
	}
}

func (a *app) copySyncFailure(err error) {
	msg := err.Error()
	if errors.Is(err, paste.ErrNeedAccess) {
		msg = "请在系统设置 → 隐私与安全性 → 辅助功能中允许 Snapover，然后重新复制并同步"
	} else if errors.Is(err, paste.ErrActivate) {
		msg = "当前应用已切换，请重新复制并同步"
	}
	a.flash = msg
	a.queueFileNotification(fileNotification{title: "复制并同步失败", body: msg})
}

func (a *app) saveSyncMode() {
	previous := a.cfg.Values.ManualSync
	a.cfg.Values.ManualSync = a.syncMode == 1
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values.ManualSync = previous
		a.syncMode = 0
		if previous {
			a.syncMode = 1
		}
		a.syncModeError = "同步方式保存失败，请检查数据目录权限后重试"
		return
	}
	a.syncModeError = ""
	a.engine.SetManualSync(a.cfg.Values.ManualSync)
}

func (a *app) syncModeSettings(c *ui.Context) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.tr("同步方式")).Bold().Grow(1)
		if ui.Segmented(c, &a.syncMode, a.tr("自动"), a.tr("手动")).Label(a.tr("同步方式")).Changed() {
			a.saveSyncMode()
		}
	})
	if a.syncMode == 1 {
		ui.Text(c, a.tr("普通复制仅保留在本机，按“复制并同步”快捷键发送。仍可接收对端内容。")).FontSize(12).TextColor(c.Theme().TextMuted)
	}
	if a.syncModeError != "" {
		ui.Text(c, a.tr(a.syncModeError)).FontSize(12).TextColor(c.Theme().Danger)
	}
}
