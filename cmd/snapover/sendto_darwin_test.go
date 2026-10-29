//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/2fa0/snapover/internal/sendto"
)

func TestMacFileRequestReachesRunningInstanceWithoutOpeningMainWindow(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	a := interfaceTestApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.root = ctx
	path := filepath.Join(t.TempDir(), "中文 ' $() 报告.txt")
	if err := os.WriteFile(path, []byte("test request"), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := make(chan struct{}, 2)
	var off func()
	mygo.RunOnMain(func() {
		if !mygo.App.RequestSingleInstanceLock() {
			t.Error("could not acquire test instance lock")
		}
		off = mygo.App.OnSecondInstance(func(args []string, wd string) {
			a.handleSecondInstance(args, wd)
			requests <- struct{}{}
		})
		if err := sendto.Refresh(); err != nil {
			t.Errorf("refresh services: %v", err)
		}
	})
	t.Cleanup(func() {
		mygo.RunOnMain(func() {
			off()
			if a.fileWin != nil {
				a.fileWin.Close()
			}
		})
	})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	forward := func(path string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, exe, "--send-file", path)
		cmd.Env = append(os.Environ(), "SNAPOVER_TEST_SEND_FILE_HELPER=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("second instance: %v %s", err, out)
		}
		select {
		case <-requests:
		case <-ctx.Done():
			t.Fatal("request did not reach running instance")
		}
	}
	forward(path)
	mygo.RunOnMain(func() {
		if a.win != nil || a.fileWin == nil || !slices.Equal(a.filePaths, []string{path}) || a.fileCommandHint == "" {
			t.Error("offline request did not retain file in the send window")
		}
		a.fileBusy = true
	})
	forward(filepath.Join(t.TempDir(), "other.txt"))
	mygo.RunOnMain(func() {
		if a.win != nil || !slices.Equal(a.filePaths, []string{path}) || !a.fileBusy {
			t.Error("busy request replaced the original file")
		}
	})
}

func TestFinderServiceUsesExistingFileRequestAndRespectsDisable(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	a := interfaceTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.root = ctx
	path := filepath.Join(t.TempDir(), "中文 ' $() 报告.txt")
	if err := os.WriteFile(path, []byte("test request"), 0o600); err != nil {
		t.Fatal(err)
	}
	mygo.RunOnMain(func() {
		a.cfg.Values.DisableSendTo = true
		if err := a.receiveFinderFiles([]string{path}, false); err == nil || a.fileWin != nil {
			t.Error("disabled service accepted the request")
		}
		a.cfg.Values.DisableSendTo = false
		if err := a.receiveFinderFiles([]string{path}, false); err != nil {
			t.Error(err)
		}
		if a.win != nil || a.fileWin == nil || !slices.Equal(a.filePaths, []string{path}) {
			t.Error("Finder request did not reach the existing file window")
		}
		a.fileBusy = true
		if err := a.receiveFinderFiles([]string{filepath.Join(t.TempDir(), "other.txt")}, false); err != nil {
			t.Error(err)
		}
		if !slices.Equal(a.filePaths, []string{path}) || !a.fileBusy || a.win != nil {
			t.Error("Finder request replaced an ongoing send")
		}
		if a.fileWin != nil {
			a.fileWin.Close()
		}
	})
}
