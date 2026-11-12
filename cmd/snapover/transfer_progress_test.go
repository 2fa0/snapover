package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	clipsync "github.com/2fa0/snapover/internal/sync"
)

func TestSmallFileTransferStaysQuiet(t *testing.T) {
	a := interfaceTestApp(t)
	a.status = "已连接"
	a.apply(clipsync.Notice{
		Kind:          clipsync.NoticeFile,
		FileClipboard: true,
		FileState:     phaseSending,
		FileName:      "note.txt",
		FileDone:      1200,
		FileTotal:     4000,
	}, nil)
	if a.transfer.kind != transferClipboardSend || a.transfer.glanced {
		t.Fatalf("small send should stay in the background: %+v", a.transfer)
	}
	tt := ui.NewTester(a.view, 960, 640)
	if tt.HasText("正在同步 note.txt") || tt.HasText("文件同步进度") {
		t.Fatal("small transfer appeared in the sidebar")
	}
	a.apply(clipsync.Notice{Kind: clipsync.NoticeFile, FileClipboard: true, FileState: phaseComplete, FileName: "note.txt", FileDone: 4000, FileTotal: 4000}, nil)
	if _, ok := a.transferStatusText(); ok || a.flash != "" {
		t.Fatalf("small success was noisy: transfer=%+v flash=%q", a.transfer, a.flash)
	}
}

func TestLargeFileTransferShowsProgressUntilPasteable(t *testing.T) {
	a := interfaceTestApp(t)
	a.status = "已连接"
	a.apply(clipsync.Notice{
		Kind:          clipsync.NoticeFile,
		FileClipboard: true,
		FileState:     phaseSending,
		FileName:      "movie.mp4",
		FileDone:      10 << 20,
		FileTotal:     40 << 20,
	}, nil)
	label, ok := a.transferStatusText()
	if !ok || label != "正在同步 movie.mp4…" {
		t.Fatalf("large send label: %q", label)
	}
	// The bar shows the share, so the label does not repeat the percentage;
	// the numbers beside the bar carry what a share cannot.
	if detail := a.transfer.detail(); detail != "10.0 MB / 40.0 MB" {
		t.Fatalf("progress detail: %q", detail)
	}
	tt := ui.NewTester(a.view, 960, 640)
	if !tt.HasText("正在同步 movie.mp4…") || !tt.HasText("文件同步进度") {
		t.Fatal("large transfer missing from sidebar")
	}
	a.apply(clipsync.Notice{Kind: clipsync.NoticeFile, FileClipboard: true, FileState: phaseWaiting, FileName: "movie.mp4", FileDone: 40 << 20, FileTotal: 40 << 20}, nil)
	label, ok = a.transferStatusText()
	if !ok || label != "对方处理中…" {
		t.Fatalf("waiting at full progress still looks finished: %q", label)
	}
	a.apply(clipsync.Notice{Kind: clipsync.NoticeFile, FileClipboard: true, FileState: phaseComplete, FileName: "movie.mp4", FileDone: 40 << 20, FileTotal: 40 << 20}, nil)
	label, ok = a.transferStatusText()
	if !ok || label != "对方已可粘贴 movie.mp4" {
		t.Fatalf("completion label: %q", label)
	}
	if a.flash != "" {
		t.Fatal("clipboard completion used receive-folder messaging")
	}
	a.transfer.hideAt = time.Now().Add(-time.Second)
	a.clearTransfer()
	if _, ok = a.transferStatusText(); ok {
		t.Fatal("lingering transfer was not cleared")
	}
}

func TestSlowSmallFileBecomesGlanceable(t *testing.T) {
	a := interfaceTestApp(t)
	a.apply(clipsync.Notice{
		Kind:          clipsync.NoticeFile,
		FileClipboard: true,
		FileState:     phasePacking,
		FileName:      "bundle.zip",
		FileTotal:     3 << 20,
	}, nil)
	a.transfer.started = time.Now().Add(-2 * time.Second)
	if !a.transfer.glanceable(time.Now()) {
		t.Fatal("slow small transfer stayed hidden")
	}
	label, ok := a.transferStatusText()
	if !ok || !strings.HasPrefix(label, "正在打包 bundle.zip") {
		t.Fatalf("packing label: %q", label)
	}
}

func TestCancelledFileTransferClearsQuietly(t *testing.T) {
	a := interfaceTestApp(t)
	a.status = "已连接"
	a.apply(clipsync.Notice{
		Kind:          clipsync.NoticeFile,
		FileClipboard: true,
		FileState:     phaseSending,
		FileName:      "movie.mp4",
		FileDone:      10 << 20,
		FileTotal:     40 << 20,
	}, nil)
	a.apply(clipsync.Notice{Kind: clipsync.NoticeSync, ClipboardState: "cancelled"}, nil)
	if a.transfer.phase != "" || a.flash != "" || a.syncFailure != "" || a.displayStatus() != "已连接" {
		t.Fatalf("cancel left UI dirty: transfer=%+v flash=%q status=%q", a.transfer, a.flash, a.displayStatus())
	}
}

func TestReceiveFolderStaysSilent(t *testing.T) {
	a := interfaceTestApp(t)
	a.apply(clipsync.Notice{Kind: clipsync.NoticeFile, FileState: "receiving", FileName: "archive.zip", FileDone: 30 << 20, FileTotal: 60 << 20}, nil)
	if _, ok := a.transferStatusText(); ok {
		t.Fatal("receive opened transfer progress")
	}
	a.apply(clipsync.Notice{Kind: clipsync.NoticeFile, FileState: "complete", FileName: "archive.zip", FilePath: "/tmp/archive.zip", FileTotal: 60 << 20}, nil)
	if a.receiveMessage != "收到 archive.zip，已保存到接收文件夹" {
		t.Fatal(a.receiveMessage)
	}
}
