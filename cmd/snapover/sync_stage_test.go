package main

import (
	"testing"

	clipsync "github.com/2fa0/snapover/internal/sync"
)

func TestSyncStageStaysOnSidebarUntilSuccess(t *testing.T) {
	a := interfaceTestApp(t)
	a.status = "已连接"
	a.apply(clipsync.Notice{Kind: clipsync.NoticeSync, ClipboardState: "history", Message: "已存入对端历史，未覆盖剪贴板"}, nil)
	if a.displayStatus() != syncStageHistory || a.syncFailure != syncStageHistory {
		t.Fatalf("history stage: %q", a.displayStatus())
	}
	a.apply(clipsync.Notice{Kind: clipsync.NoticeSync, ClipboardState: "error", Message: "写入失败"}, nil)
	if a.displayStatus() != "对方未写入剪贴板" {
		t.Fatalf("write error stage: %q", a.displayStatus())
	}
	a.apply(clipsync.Notice{Kind: clipsync.NoticeLocal, Message: "发送队列已满"}, nil)
	if a.displayStatus() != "没发出：发送队列已满" {
		t.Fatalf("local stage: %q", a.displayStatus())
	}
	a.apply(clipsync.Notice{Kind: clipsync.NoticeStatus, Message: "跳过：图片超过 20MB"}, nil)
	if a.displayStatus() != "本机跳过：图片超过 20MB" {
		t.Fatalf("skip stage: %q", a.displayStatus())
	}
	a.apply(clipsync.Notice{Kind: clipsync.NoticeSync, ClipboardState: "complete"}, nil)
	if a.displayStatus() != "已连接" || a.syncFailure != "" {
		t.Fatalf("success did not clear stage: %q", a.displayStatus())
	}
}

func TestRDPClipboardOwnerHint(t *testing.T) {
	if !rdpClipboardOwner(`C:\\Windows\\System32\\rdpclip.exe`) || rdpClipboardOwner("Snapover.exe") {
		t.Fatal("rdp owner detection")
	}
	a := interfaceTestApp(t)
	a.status = "已连接"
	a.setSyncStage("")
	if rdpClipboardOwner("rdpclip.exe") {
		a.setSyncStage(syncStageRDP)
	}
	if a.displayStatus() != syncStageRDP {
		t.Fatalf("rdp stage: %q", a.displayStatus())
	}
}
