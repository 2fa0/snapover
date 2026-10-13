package sendto

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
)

// Exercise the real Shell Link COM object in a temporary directory, without
// changing the user's SendTo menu or launching the target executable.
func TestShortcutRetainsTargetAndSendArgument(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := t.TempDir()
	exe := filepath.Join(dir, "Snapover 中文 ' $().exe")
	if err := os.WriteFile(exe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "发送文件.lnk")
	if err := createShortcut(ctx, path, exe); err != nil {
		t.Fatal(err)
	}
	output, err := runPowerShell(ctx, `
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$shell = New-Object -ComObject WScript.Shell
$link = $shell.CreateShortcut($env:SNAPOVER_SENDTO_LINK)
@{Target=$link.TargetPath; Arguments=$link.Arguments; Directory=$link.WorkingDirectory} | ConvertTo-Json -Compress
`, "SNAPOVER_SENDTO_LINK="+path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Target, Arguments, Directory string }
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatal(err)
	}
	if got.Target != exe || got.Arguments != "--send-file" || got.Directory != dir {
		t.Fatalf("shortcut = %+v", got)
	}
}

func TestShortcutCanceledDoesNotCreateFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "发送文件.lnk")
	if err := createShortcut(ctx, path, "C:\\Snapover.exe"); !errors.Is(err, context.Canceled) {
		t.Fatalf("createShortcut = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shortcut exists: %v", err)
	}
}

func TestShortcutReportsSaveFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "发送文件.lnk")
	err := createShortcut(context.Background(), path, filepath.Join(dir, "Snapover.exe"))
	if err == nil || !strings.Contains(err.Error(), "IPersistFile.Save") || !strings.Contains(err.Error(), "HRESULT") {
		t.Fatalf("createShortcut = %v", err)
	}
}

func TestShortcutHRESULT(t *testing.T) {
	for _, hr := range []uintptr{0, 1} {
		if err := shortcutError("CoInitializeEx", hr); err != nil {
			t.Fatalf("HRESULT %d: %v", hr, err)
		}
	}
	if err := shortcutError("IPersistFile.Save", 0x80070005); err == nil || !strings.Contains(err.Error(), "0x80070005") {
		t.Fatalf("error = %v", err)
	}
}

// Only the test uses PowerShell to independently inspect the saved link.
func runPowerShell(ctx context.Context, script string, env ...string) ([]byte, error) {
	chars := utf16.Encode([]rune(script))
	buf := make([]byte, len(chars)*2)
	for i, ch := range chars {
		binary.LittleEndian.PutUint16(buf[i*2:], ch)
	}
	exe := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.CommandContext(ctx, exe, "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(buf))
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("读取快捷方式失败：%w %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
