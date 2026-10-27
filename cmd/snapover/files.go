package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

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
		Title: "发送文件 · Snapover", Width: 460, Height: 340,
		MinWidth: 420, MinHeight: 300, Content: ui.View(a.filesView),
	})
	a.fileWin.OnClosed(func() { a.fileWin = nil })
}

func (a *app) filesView(c *ui.Context) {
	a.windowShortcuts(c)
	t := c.Theme()
	link, online := a.engine.Link()
	ui.Scroll(c).Fill().Background(t.Background).Padding(24).Gap(20).Children(func() {
		title := "发送文件"
		if a.fileRecipient != "" && (a.fileBusy || a.fileSent) {
			title = "发送到 " + a.fileRecipient
		} else if online {
			title = "发送到 " + displayName(link.Name, link.ID)
		}
		ui.Text(c, title).FontSize(20).Bold().MaxLines(2)
		zone := ui.Column(c.Key("file-drop")).FillWidth().Padding(18).Gap(10).Background(t.Surface).Radius(8).Disabled(a.fileBusy || a.fileChoosing)
		if a.filePath == "" {
			zone.Border(1, t.Border)
		}
		if zone.FileDragOver() {
			zone.Border(2, t.Accent)
		}
		if paths := zone.DroppedFiles(); len(paths) > 0 && !a.fileBusy && !a.fileChoosing {
			a.selectFile(paths)
		}
		zone.Children(func() {
			if a.filePath == "" {
				ui.Text(c, "把文件拖到这里").Bold()
				ui.Text(c, "单个文件，最大 50 MB").FontSize(12).TextColor(t.TextMuted)
				if ui.Button(c, "选择文件…").Disabled(a.fileBusy || a.fileChoosing).Clicked() {
					a.chooseFile()
				}
			} else {
				ui.Text(c, filepath.Base(a.filePath)).Bold().MaxLines(2).Tooltip(a.filePath)
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, formatSize(int(a.fileSize))).FontSize(12).TextColor(t.TextMuted)
					ui.Spacer(c)
					if !a.fileBusy && ui.Button(c, "更换…").Disabled(a.fileChoosing).Clicked() {
						a.chooseFile()
					}
				})
			}
		})
		if a.fileBusy && !a.fileWaiting {
			value := float64(0)
			if a.fileSize > 0 {
				value = float64(a.fileDone) / float64(a.fileSize)
			}
			ui.Progress(c, value).FillWidth().Label("文件发送进度")
		}
		// Show one relevant status instead of stacking connection and transfer hints.
		message := a.fileMessage
		switch {
		case a.fileCommandHint != "":
			message = a.fileCommandHint
		case !a.fileBusy && !a.fileSent && !online:
			if message == "" {
				message = "连接设备后即可发送"
			}
		case !a.fileBusy && !a.fileSent && !link.FileTransfer:
			message = "对方暂不支持接收文件，请更新对方的 Snapover"
		}
		if message != "" {
			ui.Text(c, message).TextColor(t.TextMuted).MaxLines(4)
		}
		if a.fileBusy {
			ui.Text(c, "关闭窗口后仍会继续传输").FontSize(12).TextColor(t.TextMuted)
			return
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			switch {
			case a.fileSent:
				if ui.PrimaryButton(c, "完成").Clicked() && a.fileWin != nil {
					a.fileWin.Close()
				}
			case !online:
				if ui.Button(c, "连接设备").Clicked() {
					a.show(pageDevices)
				}
			case a.filePath != "":
				if ui.PrimaryButton(c, "发送").Disabled(!link.FileTransfer || a.fileChoosing).Clicked() {
					a.sendSelectedFile(false)
				}
			}
		})
	})
}

func (a *app) updateFiles(fn func()) {
	a.update(fn)
}

func (a *app) selectFile(paths []string) {
	a.fileCommandHint = ""
	a.fileSent, a.fileRecipient = false, ""
	if len(paths) != 1 {
		a.filePath = ""
		a.fileMessage = "一次只能发送一个文件，请重新选择"
		return
	}
	info, err := filetransfer.Validate(paths[0], a.engine.FileLimit())
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

func (a *app) sendSelectedFile(background bool) {
	if a.fileBusy || a.filePath == "" {
		return
	}
	a.fileCommandHint = ""
	a.fileSent = false
	a.fileBusy, a.fileDone, a.fileMessage = true, 0, "正在等待对方接收…"
	path := a.filePath
	link, _ := a.engine.Link()
	a.fileRecipient = displayName(link.Name, link.ID)
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
				if background {
					a.openFiles()
				}
				return
			}
			a.fileSent, a.fileMessage = true, "已发送"
			if background && runtime.GOOS == "darwin" {
				a.sentFileNotification(name, displayName(link.Name, link.ID))
			}
		})
	}()
}

func (a *app) sentFileNotification(name, peer string) {
	a.queueFileNotification(fileNotification{title: "文件已发送 · Snapover", body: name + " 已保存到 " + peer + " 的接收文件夹"})
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
	a.queueFileNotification(fileNotification{title: "收到文件 · Snapover", body: n.FileName + " 已保存到接收文件夹", path: n.FilePath})
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
			a.status = "打开接收位置失败：" + err.Error() + "。请从文件管理器打开接收文件夹"
		})
	}
}

func (a *app) receiveSettings(c *ui.Context) {
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
