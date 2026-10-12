// Package sendto registers a per-user Windows SendTo shortcut.
package sendto

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const shortcutName = "Snapover 发送文件.lnk"

func shortcutPath() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_SendTo, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, shortcutName), nil
}

func Install(ctx context.Context) error {
	path, err := shortcutPath()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return createShortcut(ctx, path, exe)
}

func Remove() error {
	path, err := shortcutPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func createShortcut(ctx context.Context, path, exe string) error {
	// Paths are passed as environment values, never interpreted as script.
	_, err := runPowerShell(ctx, `
$ErrorActionPreference = 'Stop'
$shell = New-Object -ComObject WScript.Shell
$link = $shell.CreateShortcut($env:SNAPOVER_SENDTO_LINK)
$link.TargetPath = $env:SNAPOVER_SENDTO_EXE
$link.Arguments = '--send-file'
$link.WorkingDirectory = [IO.Path]::GetDirectoryName($env:SNAPOVER_SENDTO_EXE)
$link.IconLocation = $env:SNAPOVER_SENDTO_EXE + ',0'
$link.Description = 'Send a file with Snapover'
$link.Save()
`, "SNAPOVER_SENDTO_LINK="+path, "SNAPOVER_SENDTO_EXE="+exe)
	return err
}

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
		return nil, fmt.Errorf("创建快捷方式失败：%w %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
