package main

import (
	"testing"

	clipsync "github.com/2fa0/snapover/internal/sync"
)

func TestClipboardResultSuccessIsQuietAndDoesNotDuplicateHistory(t *testing.T) {
	a := interfaceTestApp(t)
	a.status, a.flash = "已连接", ""
	a.apply(clipsync.Notice{Kind: clipsync.NoticeSync, ClipboardState: "complete"}, nil)
	if a.status != "已连接" || a.flash != "" || a.syncStatus == "" || len(a.entries) != 0 {
		t.Fatal("success interrupted UI or duplicated history")
	}
	a.apply(clipsync.Notice{Kind: clipsync.NoticeSync, ClipboardState: "history", Message: "已存入对端历史，未覆盖剪贴板"}, nil)
	if a.displayStatus() != syncStageHistory || a.syncFailure != syncStageHistory || len(a.entries) != 0 {
		t.Fatal("history-only result was silent or duplicated history")
	}
}

func TestClipboardSuccessClearsOnlySyncFailureAndIgnoresOldSession(t *testing.T) {
	a := interfaceTestApp(t)
	a.status = "已连接"
	failure := clipsync.Notice{Kind: clipsync.NoticeSync, ClipboardState: "error", Message: "写入失败"}
	a.apply(failure, nil)
	if a.displayStatus() != "对方未写入剪贴板" || a.status != "已连接" {
		t.Fatal("failure overwrote connection status")
	}
	a.apply(clipsync.Notice{Kind: clipsync.NoticeSync, ClipboardState: "complete"}, nil)
	if a.displayStatus() != "已连接" || a.flash != "" {
		t.Fatal("success retained previous failure")
	}
	a.apply(failure, nil)
	a.flash = "其他提示"
	a.apply(clipsync.Notice{Kind: clipsync.NoticeSync, ClipboardState: "complete"}, nil)
	if a.flash != "其他提示" {
		t.Fatal("success cleared an unrelated message")
	}
	before := a.syncStatus
	failure.SessionID = "closed-session"
	a.apply(failure, nil)
	if a.syncStatus != before || a.syncFailure != "" {
		t.Fatal("old session overwrote current status")
	}
}
