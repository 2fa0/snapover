package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/2fa0/snapover/internal/filetransfer"
	"github.com/2fa0/snapover/internal/settings"
	clipsync "github.com/2fa0/snapover/internal/sync"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *app) openFiles() {
	if a.fileWin != nil {
		a.fileWin.Show()
		a.fileWin.Focus()
		return
	}
	a.fileWin = mygo.NewWindow(a.placeOnWorkingDisplay(mygo.WindowOptions{
		Title: a.tr("发送文件 · Snapover"), Width: 460, Height: 340,
		MinWidth: 420, MinHeight: 300, Content: appView(a.filesView),
	}))
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
		ui.Text(c, a.tr(title)).FontSize(20).Bold().MaxLines(2)
		zone := ui.Column(c.Key("file-drop")).FillWidth().Padding(18).Gap(10).Radius(8).Disabled(a.fileBusy || a.fileChoosing)
		over := float32(0)
		if zone.FileDragOver() {
			over = 1
		}
		over = zone.AnimateTo("over", over, ui.Snappy)
		zone.Background(t.Surface.Mix(t.Accent.Alpha(0.08), over))
		line := t.Border.Mix(t.Accent, over)
		width := 1 + over
		empty := !a.fileSelected()
		zone.Draw(func(p *ui.Painter, r ui.Rect) {
			box := ui.Rect{X: r.X + width/2, Y: r.Y + width/2, W: r.W - width, H: r.H - width}
			if empty {
				p.StrokeDashed(box, line, 8, width)
				return
			}
			p.Stroke(box, line, 8, width)
		})
		if paths := zone.DroppedFiles(); len(paths) > 0 && !a.fileBusy && !a.fileChoosing {
			a.selectFile(paths)
		}
		zone.Children(func() {
			if !a.fileSelected() {
				ui.Text(c, a.tr("把文件拖到这里")).Bold()
				if press(ui.Button(c, a.tr("选择文件…")).Label(a.tr("选择要发送的文件，可多选")).Disabled(a.fileBusy || a.fileChoosing)).Clicked() {
					a.chooseFile()
				}
			} else {
				ui.Text(c, a.fileTitle()).Bold().MaxLines(2).Tooltip(strings.Join(a.filePaths, "\n"))
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, formatSize(int(a.fileSize))).FontSize(12).TextColor(t.TextMuted)
					ui.Spacer(c)
					if !a.fileBusy && ui.Button(c, a.tr("更换…")).Disabled(a.fileChoosing).Clicked() {
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
			progressBar(c, value, toneRunning, a.tr("文件发送进度"))
		}
		// One status line: the transfer's own message, or the one limit the
		// buttons cannot show. Being offline needs no line — the button below
		// already offers to connect.
		message := a.fileMessage
		switch {
		case a.fileCommandHint != "":
			message = a.fileCommandHint
		case online && !a.fileBusy && !a.fileSent && !link.FileTransfer:
			message = "对方暂不支持接收文件，请更新对方的 Snapover"
		}
		if message != "" {
			ui.Text(c, a.tr(message)).TextColor(t.TextMuted).MaxLines(4)
		}
		if a.fileBusy {
			ui.Row(c).Gap(10).Children(func() {
				if a.fileControl != nil {
					label := "暂停"
					if a.fileControl.Paused() {
						label = "继续发送"
					}
					if ui.Button(c, a.tr(label)).Clicked() {
						a.fileControl.Pause(!a.fileControl.Paused())
						if a.fileControl.Paused() {
							a.fileMessage = "已暂停，点击继续发送可从当前位置恢复"
						}
					}
				}
				if a.fileCancel != nil && ui.Button(c, a.tr("取消传输")).Clicked() {
					a.fileCancel()
				}
			})
			ui.Text(c, a.tr("关闭窗口后仍会继续传输")).FontSize(12).TextColor(t.TextMuted)
			return
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			switch {
			case a.fileSent:
				if press(ui.PrimaryButton(c, a.tr("完成"))).Clicked() && a.fileWin != nil {
					a.fileWin.Close()
				}
			case !online:
				if ui.Button(c, a.tr("连接设备")).Clicked() {
					a.show(pageDevices)
				}
			case a.fileSelected():
				if press(ui.PrimaryButton(c, a.tr("发送")).Disabled(!link.FileTransfer || a.fileChoosing)).Clicked() {
					a.sendSelectedFile(false)
				}
			}
		})
	})
}

func (a *app) updateFiles(fn func()) {
	a.update(fn)
}

func (a *app) fileSelected() bool {
	return len(a.filePaths) > 0
}

// fileTitle keeps a batch summary readable in the narrow drop zone.
func (a *app) fileTitle() string {
	if len(a.filePaths) == 1 {
		return filepath.Base(a.filePaths[0])
	}
	shown := min(3, len(a.filePaths))
	names := make([]string, 0, shown)
	for _, path := range a.filePaths[:shown] {
		names = append(names, filepath.Base(path))
	}
	separator := "、"
	if a.isEN() {
		separator = ", "
	}
	title := a.trf("%d 个文件：%s", "%d files: %s", len(a.filePaths), strings.Join(names, separator))
	if len(a.filePaths) > shown {
		if a.isEN() {
			title += ", etc."
		} else {
			title += " 等"
		}
	}
	return title
}

func (a *app) selectFile(paths []string) {
	a.fileCommandHint = ""
	a.fileSent, a.fileRecipient = false, ""
	if len(paths) == 0 {
		a.filePaths, a.fileSize = nil, 0
		a.fileMessage = "请选择要发送的文件"
		return
	}
	sizes, err := filetransfer.ValidatePaths(paths, math.MaxInt64)
	if err != nil {
		a.filePaths, a.fileSize = nil, 0
		a.fileMessage = err.Error()
		return
	}
	var total int64
	for _, size := range sizes {
		total += size
	}
	a.filePaths, a.fileSize = append([]string(nil), paths...), total
	a.fileDone, a.fileMessage = 0, ""
}

func (a *app) chooseFile() {
	a.fileChoosing = true
	parent := a.fileWin
	title := a.tr("选择要发送的文件")
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: title, Multiple: true})
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
	if a.fileBusy || !a.fileSelected() {
		return
	}
	paths := append([]string(nil), a.filePaths...)
	a.fileCommandHint = ""
	a.fileSent = false
	a.fileBusy, a.fileDone = true, 0
	a.fileMessage = "正在等待对方接收…"
	link, _ := a.engine.Link()
	a.fileRecipient = displayName(link.Name, link.ID)
	large := a.fileSize > largeTransferBytes
	if large {
		a.openFiles()
	}
	a.fileControl = nil
	if large {
		a.fileControl = clipsync.NewFileControl()
	}
	ctx := a.root
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	a.fileCancel = cancel
	control := a.fileControl
	totalSize := a.fileSize
	go func() {
		defer cancel()
		var saved []string
		var err error
		var base int64
		for index, path := range paths {
			if large && index > 0 {
				next := clipsync.NewFileControl()
				next.Pause(control.Paused())
				control = next
				a.updateFiles(func() { a.fileControl = next })
			}
			var name string
			var fileSize int64
			onProgress := func(done, total int64) {
				fileSize = total
				done += base
				total = totalSize

				a.updateFiles(func() {
					a.fileDone, a.fileSize = done, total
					name := filepath.Base(paths[index])
					a.fileMessage = fmt.Sprintf("正在发送 %d/%d：%s · %s / %s", index+1, len(paths), name, formatSize(int(done)), formatSize(int(total)))
					if done == total {
						a.fileMessage = "传输完成，正在等待对方确认保存…"
					}
				})
			}
			if large {
				name, err = a.engine.SendFileControlled(ctx, link.ID, path, control, onProgress, func() {
					a.updateFiles(func() { a.fileMessage = "连接已断开，正在等待原设备重连后续传…" })
				})
			} else {
				name, err = a.engine.SendFile(ctx, link.ID, path, onProgress)
			}
			if err != nil {
				break
			}
			saved = append(saved, name)
			base += fileSize
		}
		a.updateFiles(func() {
			a.fileBusy = false
			a.fileCancel, a.fileControl = nil, nil
			if err != nil {
				a.fileMessage = "发送失败：" + err.Error() + "。请检查连接后重试"
				if len(saved) > 0 {
					a.fileMessage = fmt.Sprintf("已发送 %d/%d 个文件后失败：%s。请检查连接后重试", len(saved), len(paths), err)
				}
				if errors.Is(err, context.Canceled) {
					a.fileMessage = "已取消传输"
				} else if errors.Is(err, clipsync.ErrFileOutcomeUnknown) {
					a.fileMessage = err.Error()
				}
				if len(saved) > 0 && (errors.Is(err, context.Canceled) || errors.Is(err, clipsync.ErrFileOutcomeUnknown)) {
					a.fileMessage = fmt.Sprintf("已发送 %d/%d 个文件。%s", len(saved), len(paths), a.fileMessage)
				}
				if a.fileWin == nil && (large || !background) {
					title := "文件发送失败 · Snapover"
					if errors.Is(err, context.Canceled) {
						title = "传输已取消 · Snapover"
					}
					a.queueFileNotification(fileNotification{title: title, body: a.fileMessage})
				} else if background {
					a.openFiles()
				}
				return
			}
			a.fileSent = true
			if len(paths) == 1 {
				a.fileMessage = "已发送"
			} else {
				a.fileMessage = fmt.Sprintf("已发送 %d 个文件", len(saved))
			}
			if !large || a.fileWin == nil {
				a.sentFilesNotification(saved, displayName(link.Name, link.ID))
			}
		})
	}()
}

func (a *app) sentFilesNotification(names []string, peer string) {
	body := fmt.Sprintf("%d 个文件已保存到 %s 的接收文件夹", len(names), peer)
	if len(names) == 1 {
		body = names[0] + " 已保存到 " + peer + " 的接收文件夹"
	}
	a.queueFileNotification(fileNotification{title: "文件已发送 · Snapover", body: body})
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
			a.reportStatusError("打开接收位置失败：" + err.Error() + "。请从文件管理器打开接收文件夹")
		})
	}
}

func (a *app) receiveSettings(c *ui.Context) {
	ui.Text(c, a.engine.ReceiveDir()).Selectable().TextColor(c.Theme().TextMuted).MaxLines(2)
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, a.tr("更改接收文件夹…")).Clicked() {
			parent := a.win
			title := a.tr("选择接收文件夹")
			go func() {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: title, Directory: true})
				if err == nil && len(paths) > 0 {
					err = filetransfer.CheckReceiveDir(paths[0])
				}
				a.updateFiles(func() {
					if err != nil {
						a.reportStatusError("选择接收文件夹失败：" + err.Error())
						return
					}
					if len(paths) == 0 {
						return
					}
					old := a.cfg.Values.ReceiveDir
					a.cfg.Values.ReceiveDir = paths[0]
					if err := a.cfg.Save(); err != nil {
						a.cfg.Values.ReceiveDir = old
						a.reportStatusError("保存接收位置失败：" + err.Error())
						return
					}
					a.engine.SetReceiveDir(paths[0])
					a.status = "已更改接收文件夹，下一次接收生效"
				})
			}()
		}
		if ui.Button(c, a.tr("打开文件夹")).Clicked() {
			a.openReceiveFolder()
		}
	})
	a.sendToSettings(c)
}

func (a *app) saveFileLimit() {
	n, err := strconv.ParseInt(strings.TrimSpace(a.maxFileText), 10, 64)
	if err != nil || n < 1 || n > settings.MaxFileMB {
		a.maxFileError = "文件复制同步上限须为有效的正整数，单位 MB"
		return
	}
	previous := a.cfg.Values.MaxFileMB
	a.cfg.Values.MaxFileMB = n
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values.MaxFileMB = previous
		a.maxFileError = "文件复制同步上限保存失败，请检查数据目录权限后重试"
		return
	}
	a.engine.SetFileLimit(n << 20)
	a.maxFileText = strconv.FormatInt(n, 10)
	a.maxFileError = ""
	a.status = fmt.Sprintf("文件复制同步上限已设为 %d MB，下一次复制同步生效", n)
}
