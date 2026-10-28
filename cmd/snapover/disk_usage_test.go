package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestDirectoryAndBackupUsage(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"history", "history.damaged-20261028-123456", "history.damaged-20261028-123456.123", "history.damaged-not-a-backup"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "file"), []byte("hello"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(root, filepath.Join(root, "history", "outside")); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	size, err := directoryBytes(ctx, filepath.Join(root, "history"))
	if err != nil || size != 5 {
		t.Fatalf("directory usage followed links or lost files: %d %v", size, err)
	}
	count, size, err := backupBytes(ctx, filepath.Join(root, "history"))
	if err != nil || count != 2 || size != 10 {
		t.Fatalf("backup usage included unrelated folders: %d %d %v", count, size, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := directoryBytes(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatal("directory scan ignored cancellation", err)
	}
	if _, err := os.Stat(filepath.Join(root, "history.damaged-20261028-123456", "file")); err != nil {
		t.Fatal("statistics changed backup data", err)
	}
}

func TestOtherDiskUsageScansOnDemandAndCancelsOnCollapse(t *testing.T) {
	a := interfaceTestApp(t)
	t.Cleanup(a.cancelDiskUsage)
	tt := ui.NewTester(a.otherDiskUsageSettings, 850, 600)
	if a.diskUsagePending != nil || a.diskUsageValid {
		t.Fatal("collapsed storage controls scanned folders")
	}
	clickInterface(t, tt, "文件与备份占用")
	deadline := time.Now().Add(3 * time.Second)
	for !a.diskUsageValid {
		tt.Frame()
		if time.Now().After(deadline) {
			t.Fatal("expanded storage controls did not finish statistics")
		}
		time.Sleep(time.Millisecond)
	}
	if !tt.HasText("文件剪贴板缓存 0 B") || !tt.HasText("损坏历史备份 0 B") {
		t.Fatal("storage statistics were not displayed")
	}
	a.startDiskUsage()
	clickInterface(t, tt, "文件与备份占用")
	if a.diskUsagePending != nil || a.diskUsageCancel != nil {
		t.Fatal("collapsing storage controls retained a scan")
	}
}
