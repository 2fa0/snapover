package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	clipsync "github.com/2fa0/snapover/internal/sync"
)

type fileRequest struct {
	paths []string
	cold  bool
}

// SendTo appends selected paths to the shortcut's --send-file argument.
// Preserve them as separate arguments: quoting and spaces are already decoded.
func sendToPaths(args []string, workingDir string) ([]string, bool) {
	if len(args) == 0 {
		return nil, false
	}
	var paths []string
	switch {
	case args[0] == "--send-file" || args[0] == "-send-file":
		paths = append(paths, args[1:]...)
	case strings.HasPrefix(args[0], "--send-file="):
		paths = append([]string{strings.TrimPrefix(args[0], "--send-file=")}, args[1:]...)
	case strings.HasPrefix(args[0], "-send-file="):
		paths = append([]string{strings.TrimPrefix(args[0], "-send-file=")}, args[1:]...)
	default:
		return nil, false
	}
	for i, path := range paths {
		if path != "" && !filepath.IsAbs(path) {
			paths[i] = filepath.Join(workingDir, path)
		}
	}
	return paths, true
}

func (a *app) prepareFileRequest(paths []string) bool {
	if a.fileBusy || a.fileChoosing {
		a.fileCommandHint = "正在处理另一个文件，请完成后再从「发送到」菜单发送"
		return false
	}
	a.selectFile(paths)
	return a.filePath != ""
}

func (a *app) handleFileRequest(paths []string, cold bool) {
	if a.win == nil {
		if a.pendingFileRequest == nil {
			a.pendingFileRequest = &fileRequest{paths: paths, cold: cold}
		}
		return
	}
	a.openFiles()
	if !a.prepareFileRequest(paths) {
		a.fileWin.Invalidate()
		return
	}
	// Resolve the cold-launch target before checking an already online peer.
	// An incoming connection may win the race with the saved-device reconnect.
	var expectedID string
	for _, dev := range a.devices {
		if dev.Peer.Addr == a.peer {
			expectedID = dev.ID
			break
		}
	}
	if link, online := a.engine.Link(); online {
		if !canAutoSendFile(cold, expectedID, link.ID) {
			a.fileCommandHint = "当前在线设备已改变，请确认设备后点击「发送」"
			a.fileWin.Invalidate()
			return
		}
		a.sendSelectedFile()
		a.fileWin.Invalidate()
		return
	}
	a.fileCommandHint = "没有在线设备。请先连接设备，再点击「发送」"
	if !cold {
		a.fileWin.Invalidate()
		return
	}
	// A cold launch may reconnect to the last paired device. Only that known
	// device can receive automatically; a different incoming peer needs a click.
	if expectedID == "" {
		a.fileWin.Invalidate()
		return
	}
	a.fileBusy, a.fileWaiting = true, true
	a.fileCommandHint = "正在连接最近的设备，连接成功后会自动发送…"
	a.fileWin.Invalidate()
	path := a.filePath
	ctx := a.root
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		err := waitSendPeer(ctx, expectedID, a.engine.Link)
		a.updateFiles(func() {
			a.fileBusy, a.fileWaiting = false, false
			if err != nil {
				a.fileCommandHint = err.Error()
				return
			}
			link, online := a.engine.Link()
			if !online || link.ID != expectedID || a.filePath != path {
				a.fileCommandHint = "连接或文件已改变，请确认后点击「发送」"
				return
			}
			a.sendSelectedFile()
		})
	}()
}

func canAutoSendFile(cold bool, expectedID, onlineID string) bool {
	return !cold || (expectedID != "" && onlineID == expectedID)
}

func waitSendPeer(ctx context.Context, expectedID string, link func() (clipsync.Link, bool)) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return errors.New("暂时未能连接设备。请先连接设备，再点击「发送」")
		}
		if peer, online := link(); online {
			if peer.ID != expectedID {
				return errors.New("当前在线设备已改变，请确认设备后点击「发送」")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("暂时未能连接设备。请先连接设备，再点击「发送」")
		case <-ticker.C:
		}
	}
}
