package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/settings"
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

func TestFileLimitSettingAppliesAndPersists(t *testing.T) {
	a := interfaceTestApp(t)
	tt := ui.NewTester(a.settingsPage, 960, 1200)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	clickInterface(t, tt, "大小限制")
	clickInterface(t, tt, "文件大小上限，MB")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("1024")
	clickInterface(t, tt, "应用文件上限")
	got, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || got.Values.MaxFileMB != 1024 || a.engine.FileLimit() != 1024<<20 {
		t.Fatalf("file limit was not applied: %+v, %v", got, err)
	}
	for _, value := range []string{"", "abc", "0", "-1", "1.5", "8796093022208", "9223372036854775808"} {
		a.maxFileText = value
		a.saveFileLimit()
		if a.maxFileError == "" || a.cfg.Values.MaxFileMB != 1024 || a.engine.FileLimit() != 1024<<20 {
			t.Fatalf("invalid file limit %q changed the setting", value)
		}
	}
	a.maxFileText = " 4096 "
	a.saveFileLimit()
	if a.maxFileError != "" || a.maxFileText != "4096" {
		t.Fatal("valid limit did not clear validation error")
	}
	reopened, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil {
		t.Fatal(err)
	}
	b := newApp(a.engine, a.hist, reopened, &identity.Identity{}, ":47821", nil, "")
	if b.maxFileText != "4096" || b.engine.FileLimit() != 4096<<20 {
		t.Fatal("file limit was not restored on startup")
	}
	view := ui.NewTester(b.filesView, 460, 340)
	if !view.HasText("单个文件，最大 4096 MB") {
		t.Fatal("send window does not show configured limit")
	}
}

func TestFileLimitSettingAccessibleFromMainSettingsPage(t *testing.T) {
	a := interfaceTestApp(t)
	a.page = pageSettings
	tt := ui.NewTester(a.view, 960, 640)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if tt.HasText("文件大小上限，MB") || tt.HasText("更改接收文件夹…") {
		t.Fatal("file settings should stay collapsed until requested")
	}
	clickInterface(t, tt, "大小限制")
	clickInterface(t, tt, "文件大小上限，MB")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("512")
	clickInterface(t, tt, "应用文件上限")
	if a.cfg.Values.MaxFileMB != 512 || a.engine.FileLimit() != 512<<20 {
		t.Fatal("file limit could not be changed from the main settings page")
	}
}

func TestFileLimitSaveFailureKeepsActiveLimit(t *testing.T) {
	a := interfaceTestApp(t)
	if err := os.Mkdir(a.cfg.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	a.maxFileText = "1024"
	a.saveFileLimit()
	if a.maxFileError == "" || a.cfg.Values.MaxFileMB != 200 || a.engine.FileLimit() != 200<<20 {
		t.Fatal("failed save changed the active file limit")
	}
}
