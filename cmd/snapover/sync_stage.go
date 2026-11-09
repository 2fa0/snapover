package main

import (
	"strings"

	clipsync "github.com/2fa0/snapover/internal/sync"
)

const (
	syncStageHistory = "已到对方，未覆盖剪贴板（对方刚复制过）"
	syncStageRDP     = "远程桌面剪贴板盖掉了刚才的内容。请关掉该连接的剪贴板共享。"
)

func (a *app) setSyncStage(msg string) {
	a.syncFailure = msg
	if msg != "" {
		a.flash = msg
	}
}

func (a *app) applySyncStage(n clipsync.Notice) {
	switch n.Kind {
	case clipsync.NoticeSync:
		switch n.ClipboardState {
		case "complete", "cancelled":
			a.clearSyncFailure()
		case "sending":
			return
		case "history":
			a.setSyncStage(syncStageHistory)
		default:
			if n.Message == "" {
				a.setSyncStage("没发出：同步失败")
				return
			}
			if strings.Contains(n.Message, "写入") {
				a.setSyncStage("对方未写入剪贴板")
				return
			}
			a.setSyncStage("没发出：" + n.Message)
		}
	case clipsync.NoticeLocal:
		if n.Message != "" {
			a.setSyncStage("没发出：" + n.Message)
		}
	case clipsync.NoticeStatus:
		if staged, ok := syncStageFromStatus(n.Message); ok {
			a.setSyncStage(staged)
		}
	case clipsync.NoticeError:
		if strings.Contains(n.Message, "写入剪贴板失败") {
			a.setSyncStage("本机未写入剪贴板，已存历史")
		}
	case clipsync.NoticeFile:
		if n.FileClipboard && n.FileState == "error" && n.Message != "" {
			a.setSyncStage("没发出：" + n.Message)
		}
	}
}

func syncStageFromStatus(msg string) (string, bool) {
	switch {
	case msg == "跳过敏感内容":
		return "本机跳过：敏感内容", true
	case strings.HasPrefix(msg, "跳过："):
		return "本机跳过：" + strings.TrimPrefix(msg, "跳过："), true
	case strings.HasPrefix(msg, "跳过"):
		return "本机跳过：" + strings.TrimPrefix(msg, "跳过"), true
	default:
		return "", false
	}
}
