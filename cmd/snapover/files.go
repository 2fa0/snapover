package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/filetransfer"
	clipsync "github.com/2fa0/snapover/internal/sync"
)

func (a *app) openFiles() {
	if a.fileWin != nil {
		a.fileWin.Show()
		a.fileWin.Focus()
		return
	}
	a.fileWin = mygo.NewWindow(mygo.WindowOptions{
		Title: "发送文件 · Snapover", Width: 520, Height: 490,
		MinWidth: 480, MinHeight: 450, Content: ui.View(a.filesView),
	})
	a.fileWin.OnClosed(func() { a.fileWin = nil })
}

func (a *app) filesView(c *ui.Context) {
	t := c.Theme()
	link, online := a.engine.Link()
	ui.Scroll(c).Fill().Background(t.Background).Padding(24).Gap(14).Children(func() {
		ui.Text(c, "发送文件").FontSize(22).Bold()
		if online {
			ui.Text(c, "发送到 "+displayName(link.Name, link.ID)).TextColor(t.TextMuted)
		} else {
			ui.Text(c, "没有在线设备，请先在设置中连接设备。").TextColor(t.TextMuted)
		}
		if online && !link.FileTransfer {
			ui.Text(c, "对方版本不支持发送文件，请更新两边的 Snapover。").TextColor(t.TextMuted)
		}
		if a.fileCommandHint != "" {
			ui.Text(c, a.fileCommandHint).MaxLines(3)
		}
		zone := ui.Column(c.Key("file-drop")).FillWidth().Height(126).Padding(18).Gap(8).Border(1, t.Border).Radius(8).Disabled(a.fileBusy || a.fileChoosing)
		if zone.FileDragOver() {
			zone.Border(2, t.Accent)
		}
		if paths := zone.DroppedFiles(); len(paths) > 0 && !a.fileBusy && !a.fileChoosing {
			a.selectFile(paths)
		}
		zone.Children(func() {
			if a.filePath == "" {
				ui.Text(c, "把文件拖到这里").Bold()
				ui.Text(c, "单个文件，最大 50 MB").TextColor(t.TextMuted)
			} else {
				ui.Text(c, filepath.Base(a.filePath)).Bold().SingleLine()
				ui.Text(c, formatSize(int(a.fileSize))).TextColor(t.TextMuted)
			}
			if ui.Button(c, "选择文件…").Disabled(a.fileBusy || a.fileChoosing).Clicked() {
				a.chooseFile()
			}
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			label := "发送"
			if online {
				label = "发送到 " + displayName(link.Name, link.ID)
			}
			if a.fileBusy {
				label = "正在发送…"
			}
			if a.fileWaiting {
				label = "正在连接…"
			}
			if ui.PrimaryButton(c, label).Disabled(!online || !link.FileTransfer || a.filePath == "" || a.fileBusy || a.fileChoosing).Clicked() {
				a.sendSelectedFile()
			}
			if a.fileBusy {
				ui.Text(c, "关闭窗口后仍会继续传输").FontSize(12).TextColor(t.TextMuted)
			}
		})
		if a.fileBusy {
			value := float64(0)
			if a.fileSize > 0 {
				value = float64(a.fileDone) / float64(a.fileSize)
			}
			ui.Progress(c, value).FillWidth().Label("文件发送进度")
		}
		if a.fileMessage != "" {
			ui.Text(c, a.fileMessage).MaxLines(3)
		}
		if a.receiveMessage != "" {
			ui.Text(c, a.receiveMessage).FontSize(12).TextColor(t.TextMuted).MaxLines(2)
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "打开接收文件夹").Clicked() {
				a.openReceiveFolder()
			}
			if a.lastReceived != "" && ui.Button(c, "查看收到的文件").Clicked() {
				path := a.lastReceived
				go mygo.Shell.ShowItemInFolder(path)
			}
		})
		ui.Text(c, "接收位置："+a.engine.ReceiveDir()).FontSize(12).TextColor(t.TextMuted).MaxLines(2)
	})
}

func (a *app) updateFiles(fn func()) {
	mygo.RunOnMain(func() {
		fn()
		if a.fileWin != nil {
			a.fileWin.Invalidate()
		}
		if a.win != nil {
			a.win.Invalidate()
		}
	})
}

func (a *app) selectFile(paths []string) {
	a.fileCommandHint = ""
	if len(paths) != 1 {
		a.filePath = ""
		a.fileMessage = "一次只能发送一个文件，请重新选择"
		return
	}
	info, err := filetransfer.Validate(paths[0])
	if err != nil {
		a.fileMessage = err.Error()
		a.filePath = ""
		return
	}
	a.filePath, a.fileSize = paths[0], info.Size()
	a.fileDone, a.fileMessage = 0, ""
}

func (a *app) chooseFile() {
	a.fileChoosing = true
	parent := a.fileWin
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: "选择要发送的文件"})
		a.updateFiles(func() {
			a.fileChoosing = false
			if err != nil {
				a.fileMessage = "选择文件失败：" + err.Error()
				return
			}
			if len(paths) > 0 {
				a.selectFile(paths)
			}
		})
	}()
}

func (a *app) sendSelectedFile() {
	if a.fileBusy || a.filePath == "" {
		return
	}
	a.fileCommandHint = ""
	a.fileBusy, a.fileDone, a.fileMessage = true, 0, "正在等待对方接收…"
	path := a.filePath
	link, _ := a.engine.Link()
	ctx := a.root
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		name, err := a.engine.SendFile(ctx, link.ID, path, func(done, total int64) {
			a.updateFiles(func() {
				a.fileDone, a.fileSize = done, total
				a.fileMessage = fmt.Sprintf("已传输 %s / %s", formatSize(int(done)), formatSize(int(total)))
				if done == total {
					a.fileMessage = "传输完成，正在等待对方确认保存…"
				}
			})
		})
		a.updateFiles(func() {
			a.fileBusy = false
			if err != nil {
				a.fileMessage = "发送失败：" + err.Error() + "。请检查连接后重试"
				return
			}
			a.fileMessage = "已保存到 " + displayName(link.Name, link.ID) + " 的接收文件夹：" + name
		})
	}()
}

func receivedMessage(n clipsync.Notice) string {
	if n.FileState == "complete" {
		return "收到 " + n.FileName + "，已保存到接收文件夹"
	}
	if n.FileState == "error" {
		return "接收失败：" + n.Message
	}
	return fmt.Sprintf("正在接收 %s · %s / %s", n.FileName, formatSize(int(n.FileDone)), formatSize(int(n.FileTotal)))
}

func (a *app) receivedNotification(n clipsync.Notice) {
	if !mygo.NotificationsSupported() {
		return
	}
	go func() {
		notice := mygo.NewNotification(mygo.NotificationOptions{Title: "收到文件 · Snapover", Body: n.FileName + " 已保存到接收文件夹"})
		notice.OnClick(func() { go mygo.Shell.ShowItemInFolder(n.FilePath) })
		if err := notice.Show(); err != nil && a.engine.Log != nil {
			a.engine.Log.Debug("文件通知未显示", "err", err)
		}
	}()
}

func (a *app) openReceiveFolder() {
	dir := a.engine.ReceiveDir()
	go func() {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			a.shellResult(err)
			return
		}
		a.shellResult(mygo.Shell.OpenPath(dir))
	}()
}

func (a *app) shellResult(err error) {
	if err != nil {
		a.updateFiles(func() {
			a.fileMessage = "打开接收位置失败：" + err.Error() + "。请从文件管理器打开接收文件夹"
		})
	}
}

func (a *app) receiveSettings(c *ui.Context) {
	section(c, "接收文件")
	ui.Text(c, a.engine.ReceiveDir()).Selectable().TextColor(c.Theme().TextMuted).MaxLines(2)
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, "更改接收文件夹…").Clicked() {
			parent := a.win
			go func() {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: "选择接收文件夹", Directory: true})
				if err == nil && len(paths) > 0 {
					err = filetransfer.CheckReceiveDir(paths[0])
				}
				a.updateFiles(func() {
					if err != nil {
						a.status = "选择接收文件夹失败：" + err.Error()
						return
					}
					if len(paths) == 0 {
						return
					}
					old := a.cfg.Values.ReceiveDir
					a.cfg.Values.ReceiveDir = paths[0]
					if err := a.cfg.Save(); err != nil {
						a.cfg.Values.ReceiveDir = old
						a.status = "保存接收位置失败：" + err.Error()
						return
					}
					a.engine.SetReceiveDir(paths[0])
					a.status = "已更改接收文件夹，下一次接收生效"
				})
			}()
		}
		if ui.Button(c, "打开文件夹").Clicked() {
			a.openReceiveFolder()
		}
	})
	a.sendToSettings(c)
}
