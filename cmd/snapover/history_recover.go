package main

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/history"
)

func (a *app) historyUnavailable(c *ui.Context) bool {
	if a.hist == nil || a.hist.Disabled() == nil {
		return false
	}
	t := c.Theme()
	ui.Text(c, "历史不可用，连接、发送和设置仍可使用。").FontSize(12).TextColor(t.TextMuted)
	ui.Text(c, a.hist.Disabled().Error()).FontSize(12).TextColor(t.Danger)
	ui.Text(c, "损坏文件会原样保留，确认后才移到一旁并重建。").FontSize(12).TextColor(t.TextMuted)
	if ui.Button(c, "移到一旁并重建").Clicked() {
		a.recoverHistory()
	}
	if a.historyCapacityError != "" {
		ui.Text(c, a.historyCapacityError).FontSize(12).TextColor(t.Danger)
	}
	return true
}

func (a *app) recoverHistory() {
	if a.hist == nil || a.hist.Disabled() == nil || a.historyCleanupPending != nil {
		return
	}
	dir := a.hist.Dir()
	quarantined, err := history.Quarantine(dir)
	if err != nil {
		a.historyCapacityError = "无法移开损坏历史：" + err.Error()
		return
	}
	next, err := history.Open(dir)
	if err != nil {
		a.hist = history.Unavailable(dir, err)
		a.historyCapacityError = "重建历史失败：" + err.Error()
		return
	}
	if a.cfg != nil {
		if err := next.SetCapacity(int64(a.cfg.Values.HistoryMaxMB) << 20); err != nil {
			a.historyCapacityError = "历史上限未能应用：" + err.Error()
		}
	}
	a.hist = next
	a.refreshEntries()
	a.historyCapacityError = ""
	a.status = "已将损坏历史移到 " + filepath.Base(quarantined)
}
