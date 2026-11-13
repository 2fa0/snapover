package main

import (
	"github.com/2fa0/snapover/internal/settings"
	clipsync "github.com/2fa0/snapover/internal/sync"
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

	a.filePaths, a.fileSize = []string{"报告.pdf"}, 1024
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

func TestFilesViewSummarizesBatchSelection(t *testing.T) {
	a := interfaceTestApp(t)
	tt := ui.NewTester(a.filesView, 460, 310)
	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"一.txt", "二.txt", "三.txt", "四.txt"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	a.selectFile(paths)
	if len(a.filePaths) != 4 || a.fileSize != 28 {
		t.Fatalf("batch not selected: %v %d", a.filePaths, a.fileSize)
	}
	tt.Frame()
	// The narrow drop zone names a few entries and counts the rest.
	if !tt.HasText("4 个文件：一.txt、二.txt、三.txt 等") || !tt.HasText("28 B") || !tt.HasText("更换…") {
		t.Fatal("batch selection is missing its summary or size")
	}
	// A folder anywhere in the batch keeps the previous selection untouched.
	a.selectFile([]string{paths[0], dir})
	if a.fileSelected() || a.fileMessage == "" {
		t.Fatal("folder in batch replaced the selection without a reason")
	}
	tt.Frame()
	if a.fileMessage == "" {
		t.Fatal("invalid batch did not report a reason")
	}
}

func TestExplicitFilesHaveNoSizeLimitControl(t *testing.T) {
	a := interfaceTestApp(t)
	tt := ui.NewTester(a.settingsPage, 960, 1200)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if tt.HasText("文件大小上限，MB") {
		t.Fatal("obsolete file size control")
	}
	view := ui.NewTester(a.filesView, 460, 340)
	if !view.HasText("选择文件…") {
		t.Fatal("drop zone lost its file picker")
	}
}

func TestFileTransferControls(t *testing.T) {
	a := interfaceTestApp(t)
	a.filePaths = []string{"large.bin"}
	a.fileSize = 30 << 20
	a.fileBusy = true
	a.fileControl = clipsync.NewFileControl()
	cancelled := false
	a.fileCancel = func() { cancelled = true }
	tt := ui.NewTester(a.filesView, 460, 500)
	clickInterface(t, tt, "暂停")
	if !a.fileControl.Paused() {
		t.Fatal("pause button did not pause")
	}
	clickInterface(t, tt, "继续发送")
	if a.fileControl.Paused() {
		t.Fatal("resume button did not resume")
	}
	clickInterface(t, tt, "取消传输")
	if !cancelled {
		t.Fatal("cancel button did not cancel")
	}
}

func TestFileClipboardLimitSetting(t *testing.T) {
	a := interfaceTestApp(t)
	if a.maxFileText != "20" || a.engine.FileLimit() != 20<<20 {
		t.Fatal("clipboard default is not 20 MB")
	}
	tt := ui.NewTester(a.settingsPage, 960, 1200)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	clickInterface(t, tt, "文件复制同步上限，MB")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("64")
	clickInterface(t, tt, "应用文件复制同步上限")
	cfg, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || cfg.Values.MaxFileMB != 64 || a.engine.FileLimit() != 64<<20 {
		t.Fatalf("clipboard limit not saved: %+v %v", cfg, err)
	}
	// Explicit selection must ignore even a tiny clipboard limit.
	a.engine.SetFileLimit(1)
	path := filepath.Join(t.TempDir(), "explicit.bin")
	if err := os.WriteFile(path, []byte("larger than clipboard limit"), 0600); err != nil {
		t.Fatal(err)
	}
	a.selectFile([]string{path})
	if !a.fileSelected() {
		t.Fatal("clipboard limit restricted explicit send")
	}
	a.maxFileText = "0"
	a.saveFileLimit()
	if a.maxFileError == "" || a.cfg.Values.MaxFileMB != 64 || a.engine.FileLimit() != 1 {
		t.Fatal("invalid value changed clipboard limit")
	}
}

func TestFileClipboardLimitSaveFailure(t *testing.T) {
	a := interfaceTestApp(t)
	if err := os.Mkdir(a.cfg.Path, 0700); err != nil {
		t.Fatal(err)
	}
	a.maxFileText = "64"
	a.saveFileLimit()
	if a.maxFileError == "" || a.cfg.Values.MaxFileMB != 20 || a.engine.FileLimit() != 20<<20 {
		t.Fatal("failed save changed clipboard limit")
	}
}
