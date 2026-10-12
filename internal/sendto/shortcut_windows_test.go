package sendto

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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
