package main

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *app) backupSettings(c *ui.Context) {
	ui.Collapsible(c, a.tr("置顶备份"), &a.showBackup, func() { a.backupControls(c) })
}

func (a *app) backupControls(c *ui.Context) {
	ui.Text(c, a.tr("按最新到最旧的顺序导出；范围留空表示全部。大备份可填写起止序号分批导出，每批最多 1 GiB。")).FontSize(12)
	ui.Row(c).Gap(8).Children(func() {
		a.numberInput(c.Key("backup-first"), &a.backupFirst, a.tr("起始序号"), 1, math.MaxInt64, 116)
		a.numberInput(c.Key("backup-last"), &a.backupLast, a.tr("结束序号"), 1, math.MaxInt64, 116)
	})

	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, a.tr("导出置顶")).Disabled(a.backupBusy || a.historyCleanupPending != nil).Clicked() {
			a.choosePinnedBackup(false)
		}
		if ui.Button(c, a.tr("导入置顶")).Disabled(a.backupBusy || a.historyCleanupPending != nil).Clicked() {
			a.choosePinnedBackup(true)
		}
	})
	ui.Text(c, a.tr("备份包含置顶文本、图片和备注，不含文件正文；备份未加密，请妥善保管。导入会追加记录，容量不足时按历史规则清理未置顶内容。")).FontSize(12).TextColor(c.Theme().TextMuted)
	if a.backupBusy {
		ui.Text(c, a.tr("正在处理置顶备份…")).FontSize(12)
	}
	if a.backupMessage != "" {
		ui.Text(c, a.backupMessage).FontSize(12)
	}
}

func (a *app) choosePinnedBackup(importing bool) {
	if a.backupBusy {
		return
	}
	first, last := 0, 0
	if !importing && (a.backupFirst != "" || a.backupLast != "") {
		var err error
		first, err = strconv.Atoi(a.backupFirst)
		if err == nil {
			last, err = strconv.Atoi(a.backupLast)
		}
		if err != nil || first < 1 || last < first {
			a.backupMessage = a.tr("请填写有效的起止序号")
			return
		}
	}
	a.backupBusy, a.backupMessage = true, ""
	parent, store := a.win, a.hist
	ctx := a.root
	if ctx == nil {
		ctx = context.Background()
	}
	exportTitle, importTitle := a.tr("导出置顶"), a.tr("导入置顶")
	completed, failed := a.tr("置顶备份已完成：%d 条"), a.tr("置顶备份未完成，已处理 %d 条：%s")
	go func() {
		var path string
		var err error
		if importing {
			var paths []string
			paths, err = mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: importTitle})
			if len(paths) > 0 {
				path = paths[0]
			}
		} else {
			path, err = mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: parent, Title: exportTitle, DefaultPath: "Snapover-pins.zip"})
		}
		n := 0
		if err == nil && path != "" {
			if importing {
				n, err = store.ImportPinned(ctx, path)
			} else if first > 0 {
				n, err = store.ExportPinnedRange(ctx, path, first, last)
			} else {
				n, err = store.ExportPinned(ctx, path)
			}
		}
		a.update(func() {
			a.backupBusy = false
			if err != nil {
				a.backupMessage = fmt.Sprintf(failed, n, err.Error())
			} else if path != "" {
				a.backupMessage = fmt.Sprintf(completed, n)
			}
			if importing {
				a.refreshEntries()
			}
		})
	}()
}
