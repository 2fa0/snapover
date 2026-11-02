package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type diskUsageResult struct {
	receiveDir                       string
	cache, received, backups         int64
	backupCount                      int
	cacheErr, receivedErr, backupErr error
}

func directoryBytes(ctx context.Context, dir string) (int64, error) {
	dir, err := filepath.EvalSymlinks(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var bytes int64
	err = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			bytes += info.Size()
		}
		return nil
	})
	return bytes, err
}

func backupBytes(ctx context.Context, historyDir string) (int, int64, error) {
	parent := filepath.Dir(historyDir)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return 0, 0, err
	}
	var count int
	var bytes int64
	for _, entry := range entries {
		stamp, ok := strings.CutPrefix(entry.Name(), filepath.Base(historyDir)+".damaged-")
		if !ok || !entry.IsDir() {
			continue
		}
		layout := "20060102-150405"
		if strings.Contains(stamp, ".") {
			layout += ".000"
		}
		parsed, err := time.Parse(layout, stamp)
		if err != nil || parsed.Format(layout) != stamp {
			continue
		}
		size, err := directoryBytes(ctx, filepath.Join(parent, entry.Name()))
		if err != nil {
			return count, bytes, err
		}
		count++
		bytes += size
	}
	return count, bytes, ctx.Err()
}

func (a *app) cancelDiskUsage() {
	if a.diskUsageCancel != nil {
		a.diskUsageCancel()
	}
	a.diskUsageCancel, a.diskUsagePending = nil, nil
	a.diskUsageValid = false
}

func (a *app) startDiskUsage() {
	a.cancelDiskUsage()
	ctx := a.root
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, a.diskUsageCancel = context.WithCancel(ctx)
	results := make(chan diskUsageResult, 1)
	a.diskUsagePending = results
	historyDir, receiveDir := a.hist.Dir(), a.engine.ReceiveDir()
	go func() {
		result := diskUsageResult{receiveDir: receiveDir}
		result.cache, result.cacheErr = directoryBytes(ctx, filepath.Join(filepath.Dir(historyDir), "clipboard-files"))
		result.received, result.receivedErr = directoryBytes(ctx, receiveDir)
		result.backupCount, result.backups, result.backupErr = backupBytes(ctx, historyDir)
		results <- result
	}()
}

func (a *app) otherDiskUsageSettings(c *ui.Context) {
	ui.Collapsible(c, a.tr("占用明细"), &a.showDiskUsage, func() {
		ui.Column(c).Padding(12, 0).Gap(12).Children(func() {
			if a.historyUsageValid && a.historyUsageError == "" {
				a.storageUsageLine(c, "置顶内容", a.historyUsage.PinnedBytes, nil).Tooltip(a.tr("已包含在历史内容占用中"))
				a.storageUsageLine(c, "历史目录", a.historyUsage.TotalBytes, nil).Tooltip(a.tr("包含历史内容、索引、日志和临时查看副本"))
				ui.Divider(c)
			}
			if a.diskUsageValid && a.diskUsage.receiveDir != a.engine.ReceiveDir() {
				a.cancelDiskUsage()
			}
			if !a.diskUsageValid && a.diskUsagePending == nil {
				a.startDiskUsage()
			}
			if pending := a.diskUsagePending; pending != nil {
				select {
				case result := <-pending:
					a.diskUsage, a.diskUsageValid = result, true
					a.diskUsageCancel()
					a.diskUsageCancel, a.diskUsagePending = nil, nil
				default:
				}
			}
			if a.diskUsagePending != nil {
				ui.Text(c, a.tr("正在统计文件与备份占用…")).FontSize(12).TextColor(c.Theme().TextMuted)
				c.After(50 * time.Millisecond)
			} else if a.diskUsageValid {
				a.storageUsageLine(c, "文件剪贴板缓存", a.diskUsage.cache, a.diskUsage.cacheErr)
				a.storageUsageLine(c, "接收文件夹", a.diskUsage.received, a.diskUsage.receivedErr)
				label := "损坏历史备份"
				if a.diskUsage.backupCount > 0 {
					label = fmt.Sprintf("损坏历史备份（%d 份）", a.diskUsage.backupCount)
				}
				a.storageUsageLine(c, label, a.diskUsage.backups, a.diskUsage.backupErr)
			}
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, a.tr("打开接收文件夹")).Clicked() {
					a.openReceiveFolder()
				}
				if a.diskUsageValid && a.diskUsage.backupCount > 0 && ui.Button(c, a.tr("整理历史备份")).Tooltip(a.tr("打开数据目录，查看 history.damaged-* 备份")).Clicked() {
					dir := filepath.Dir(a.hist.Dir())
					go func() {
						if err := mygo.Shell.OpenPath(dir); err != nil {
							a.update(func() {
								a.status = "打开历史备份位置失败，请从文件管理器打开数据目录：" + err.Error()
							})
						}
					}()
				}
			})
		})
	})
	if !a.showDiskUsage && a.diskUsagePending != nil {
		a.cancelDiskUsage()
	}
}

func (a *app) storageUsageLine(c *ui.Context, label string, bytes int64, err error) ui.Element {
	return ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.tr(label)).Grow(1).FontSize(12).TextColor(c.Theme().TextMuted)
		if err != nil {
			message := a.tr(label + "无法统计，请检查文件夹访问权限后刷新。")
			ui.Text(c, a.tr("统计失败")).FontSize(12).TextColor(c.Theme().Warning).Label(message).Tooltip(message)
		} else {
			ui.Text(c, storageSize(bytes)).FontSize(12).FontFeatures("tnum")
		}
	})
}
