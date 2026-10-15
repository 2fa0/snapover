package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/settings"
)

type historyUsageResult struct {
	usage   history.Usage
	version uint64
	err     error
}

type historyCleanupResult struct {
	cleared bool
	err     error
}

func (a *app) historyStorageSettings(c *ui.Context) {
	a.pollHistoryStorage()
	t := c.Theme()
	busy := a.historyCleanupPending != nil
	ui.Text(c, "未置顶历史最多保留 1000 条或 30 天；容量超限时从最旧记录开始清理。置顶内容始终保留，并计入容量。").FontSize(12).TextColor(t.TextMuted)
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "历史内容容量上限").Grow(1).Bold()
		input := ui.TextInput(c.Key("history-max-mb"), &a.historyMaxText).Label("历史容量上限，MB").Width(84)
		if input.Submitted() && !busy {
			a.saveHistoryCapacity()
		}
		ui.Text(c, "MB").TextColor(t.TextMuted)
		if ui.Button(c, "应用并清理").Disabled(busy).Clicked() {
			a.saveHistoryCapacity()
		}
	})
	ui.Text(c, "包含文本、原图、缩略图和图片预览。应用上限会立即清理超出的未置顶记录。").FontSize(12).TextColor(t.TextMuted)
	ui.Row(c.Key("history-storage-actions")).Gap(12).Children(func() {
		if ui.Button(c, "刷新占用").Disabled(busy || a.historyUsagePending != nil).Clicked() {
			a.historyUsageValid = false
		}
		label := "清理全部未置顶历史"
		if a.confirmStorageClear {
			label = "确认清理未置顶历史"
		}
		if ui.Button(c, label).Disabled(busy || len(a.entries) == 0).Clicked() {
			if a.confirmStorageClear {
				a.confirmStorageClear = false
				a.startHistoryCleanup(true, 0)
			} else {
				a.confirmStorageClear = true
			}
		}
		if a.confirmStorageClear && ui.Button(c, "取消清理").Clicked() {
			a.confirmStorageClear = false
		}
	})
	ui.Column(c.Key("history-storage-usage")).Gap(4).Children(func() {
		if a.historyUsageValid && a.historyUsageError == "" {
			ui.Textf(c, "历史内容 %s，其中置顶 %s", storageSize(a.historyUsage.ContentBytes), storageSize(a.historyUsage.PinnedBytes)).FontSize(12).TextColor(t.TextMuted)
			ui.Textf(c, "历史目录共占用 %s，含索引和临时查看副本", storageSize(a.historyUsage.TotalBytes)).FontSize(12).TextColor(t.TextMuted)
			if a.historyUsage.PinnedBytes > int64(a.cfg.Values.HistoryMaxMB)<<20 {
				ui.Text(c, "置顶内容已超过容量上限，请取消部分置顶或手动删除。").FontSize(12).TextColor(t.Danger)
			}
		} else if a.historyUsagePending != nil {
			ui.Text(c, "正在统计占用…").FontSize(12).TextColor(t.TextMuted)
		}
	})
	if a.historyCapacityError != "" {
		ui.Text(c, a.historyCapacityError).FontSize(12).TextColor(t.Danger)
	}
	if a.historyUsageError != "" {
		ui.Text(c, a.historyUsageError).FontSize(12).TextColor(t.Danger)
	}
	if busy {
		ui.Text(c, "正在清理历史…").FontSize(12).TextColor(t.TextMuted)
	}
	if !a.historyUsageValid && a.historyUsagePending == nil && !busy {
		results := make(chan historyUsageResult, 1)
		a.historyUsagePending = results
		store, version := a.hist, a.historyUsageVersion
		go func() { usage, err := store.Usage(); results <- historyUsageResult{usage, version, err} }()
	}
	if a.historyUsagePending != nil || a.historyCleanupPending != nil {
		c.After(50 * time.Millisecond)
	}
}

func storageSize(bytes int64) string {
	if bytes >= 1<<30 {
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(1<<30))
	}
	if bytes >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	}
	if bytes >= 1<<10 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	}
	return fmt.Sprintf("%d B", bytes)
}

func (a *app) saveHistoryCapacity() {
	if a.historyCleanupPending != nil {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(a.historyMaxText))
	if err != nil || n < 1 || n > settings.MaxHistoryMB {
		a.historyCapacityError = "历史容量上限须为 1–1048576 的整数，单位 MB"
		return
	}
	before := a.cfg.Values.HistoryMaxMB
	a.cfg.Values.HistoryMaxMB = n
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values.HistoryMaxMB = before
		a.historyCapacityError = "历史上限保存失败，请检查数据目录权限后重试"
		return
	}
	a.historyMaxText = strconv.Itoa(n)
	a.startHistoryCleanup(false, int64(n)<<20)
}

func (a *app) startHistoryCleanup(clear bool, limit int64) {
	if a.historyCleanupPending != nil {
		return
	}
	// Cancel prepared paste callbacks before deleting their source records.
	a.historyPaste = nil
	a.pickBusy = false
	results := make(chan historyCleanupResult, 1)
	a.historyCleanupPending, a.historyCapacityError = results, ""
	store := a.hist
	go func() {
		var err error
		if clear {
			err = store.Clear()
		} else {
			err = store.SetCapacity(limit)
		}
		results <- historyCleanupResult{clear, err}
	}()
}

func (a *app) pollHistoryStorage() {
	if pending := a.historyCleanupPending; pending != nil {
		select {
		case result := <-pending:
			a.historyCleanupPending = nil
			// A delete can durably remove records before a locked file reports an error.
			a.refreshEntries()
			a.histSel = -1
			if result.err != nil {
				a.historyCapacityError = "历史清理失败，请检查数据目录权限后重新应用：" + result.err.Error()
			} else {
				a.status = "历史容量上限已应用，超限的未置顶记录已清理"
				if result.cleared {
					a.status = "已清理全部未置顶历史"
				}
			}
		default:
		}
	}
	if pending := a.historyUsagePending; pending != nil {
		select {
		case result := <-pending:
			a.historyUsagePending = nil
			if result.version != a.historyUsageVersion {
				return
			}
			if result.err != nil {
				a.historyUsageError = "无法统计历史占用，请检查数据目录权限后刷新：" + result.err.Error()
				// Keep the last result until an explicit refresh, avoiding a retry loop.
				a.historyUsageValid = true
			} else {
				a.historyUsage, a.historyUsageValid = result.usage, true
				a.historyUsageError = ""
			}
		default:
		}
	}
}
