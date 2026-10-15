package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestFilesViewFollowsTransferState(t *testing.T) {
	a := interfaceTestApp(t)
	a.receiveMessage = "收到 other.pdf，已保存到接收文件夹"
	tt := ui.NewTester(a.filesView, 460, 310)
	if !tt.HasText("选择文件…") || !tt.HasText("连接设备") {
		t.Fatal("empty window needs file selection and a way to connect")
	}
	for _, label := range []string{"发送", "完成", "收到 other.pdf", "接收位置", "打开接收文件夹"} {
		if _, ok := tt.Find(label); ok {
			t.Fatalf("empty window includes unrelated action or receiving information: %s", label)
		}
	}
	if tt.HasText("收到 other.pdf") || tt.HasText(a.engine.ReceiveDir()) {
		t.Fatal("receiving information leaked into the sending window")
	}

	a.filePath, a.fileSize = "报告.pdf", 1024
	a.fileBusy, a.fileRecipient = true, "doze-mac"
	a.fileMessage = "已传输 512 B / 1 KB"
	tt.Frame()
	if !tt.HasText("发送到 doze-mac") || !tt.HasText("文件发送进度") || !tt.HasText(a.fileMessage) {
		t.Fatal("active transfer lost its recipient or progress")
	}
	for _, label := range []string{"更换…", "发送", "连接设备", "完成"} {
		if _, ok := tt.Find(label); ok {
			t.Fatalf("active transfer exposes an unavailable action: %s", label)
		}
	}

	// The success receipt still names the recipient even after it disconnects.
	a.fileBusy, a.fileSent, a.fileMessage = false, true, "已发送"
	tt.Frame()
	if !tt.HasText("发送到 doze-mac") || !tt.HasText("已发送") || !tt.HasText("完成") {
		t.Fatal("completed transfer lost its receipt")
	}
	if tt.HasText("连接设备") || tt.HasText("文件发送进度") {
		t.Fatal("completed transfer still shows connection or progress controls")
	}

	path := filepath.Join(t.TempDir(), "next.txt")
	if err := os.WriteFile(path, []byte("next"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.selectFile([]string{path})
	tt.Frame()
	if a.fileSent || a.fileRecipient != "" || tt.HasText("已发送") || tt.HasText("完成") {
		t.Fatal("selecting another file retained the previous success state")
	}
	if !tt.HasText("next.txt") || !tt.HasText("更换…") {
		t.Fatal("selected file is missing its summary or replacement action")
	}
}
