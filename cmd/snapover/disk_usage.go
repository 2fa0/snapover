package main

import (
	"context"
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
	ui.Collapsible(c, "文件与备份占用", &a.showDiskUsage, func() {
		ui.Column(c).Padding(12, 0).Gap(8).Children(func() {
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
				ui.Text(c, "正在统计文件与备份占用…").FontSize(12).TextColor(c.Theme().TextMuted)
				c.After(50 * time.Millisecond)
			} else if a.diskUsageValid {
				storageUsageLine(c, "文件剪贴板缓存", a.diskUsage.cache, a.diskUsage.cacheErr)
				storageUsageLine(c, "接收文件夹", a.diskUsage.received, a.diskUsage.receivedErr)
				storageUsageLine(c, "损坏历史备份", a.diskUsage.backups, a.diskUsage.backupErr)
				ui.Text(c, "接收文件和历史备份不会自动删除，请在文件夹中确认后整理。").FontSize(12).TextColor(c.Theme().TextMuted)
			}
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "刷新文件占用").Disabled(a.diskUsagePending != nil).Clicked() {
					a.startDiskUsage()
				}
				if ui.Button(c, "打开接收文件夹").Clicked() {
					a.openReceiveFolder()
				}
				if a.diskUsageValid && a.diskUsage.backupCount > 0 && ui.Button(c, "整理历史备份").Clicked() {
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
			if a.diskUsageValid && a.diskUsage.backupCount > 0 {
				ui.Textf(c, "共 %d 份备份，位于数据目录的 history.damaged-* 文件夹。", a.diskUsage.backupCount).FontSize(12).TextColor(c.Theme().TextMuted)
			}
		})
	})
	if !a.showDiskUsage && a.diskUsagePending != nil {
		a.cancelDiskUsage()
	}
}

func storageUsageLine(c *ui.Context, label string, bytes int64, err error) {
	if err != nil {
		ui.Text(c, label+"无法统计，请检查文件夹访问权限后刷新。").FontSize(12).TextColor(c.Theme().Warning)
		return
	}
	ui.Textf(c, "%s %s", label, storageSize(bytes)).FontSize(12).TextColor(c.Theme().TextMuted)
}
