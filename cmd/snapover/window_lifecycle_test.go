package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/discovery"
	clipsync "github.com/2fa0/snapover/internal/sync"
)

// SNAPOVER_TEST_UI=1 go test ./cmd/snapover -run TestMainWindow -count=1
// runs the lifecycle checks with real windows in a desktop session.
func TestMain(m *testing.M) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "snapover-ui-test-")
	if err != nil {
		panic(err)
	}
	mygo.App.SetName("Snapover UI Tests")
	mygo.App.SetPath(mygo.PathUserData, dir)
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {})
	result := make(chan int, 1)
	mygo.App.WhenReady(func() {
		go func() {
			result <- m.Run()
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	_ = os.RemoveAll(dir)
	os.Exit(<-result)
}

func TestMainWindowClosesAndReopensWithoutStoppingBackgroundUpdates(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	a := interfaceTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	board := &pasteTestBoard{}
	a.root, a.board = ctx, board
	item, _ := clip.FromText("background history")
	entry, err := a.hist.Add("local", "", item)
	if err != nil {
		t.Fatal(err)
	}
	var first *mygo.Window
	var request *pasteRequest
	var searchCtx context.Context
	bounds := mygo.Rectangle{X: 120, Y: 140, Width: 880, Height: 600}
	mygo.RunOnMain(func() {
		a.refreshEntries()
		a.initThumbnails()
		a.reveal(pageHistory)
		first = a.win
		first.SetBounds(bounds)
		if _, err := first.CapturePage(); err != nil {
			t.Errorf("render initial history: %v", err)
		}
		a.confirm, a.confirmClear = true, true
		searchCtx, a.historySearch.cancel = context.WithCancel(ctx)
		request = &pasteRequest{id: entry.ID, result: make(chan pasteResult, 1)}
		request.result <- pasteResult{item: item}
		a.historyPaste = request
		first.Close()
		if !first.IsDestroyed() || a.win != nil || len(mygo.Windows()) != 0 {
			t.Error("close retained the main window")
		}
		if searchCtx.Err() == nil || a.historyPaste != nil || a.confirm || a.confirmClear {
			t.Error("close retained search, paste or confirmation state")
		}
		if a.thumbs.Len() != 0 || a.thumbTimer != nil {
			t.Error("close retained idle thumbnails")
		}
		a.completeHistoryPaste(request)
		if len(board.writes) != 0 {
			t.Error("closed history wrote a late result to the clipboard")
		}
	})
	// These callbacks arrive from worker goroutines while no window exists.
	a.onNotice(clipsync.Notice{Kind: clipsync.NoticeRemote, Item: item, PeerName: "other computer"})
	a.onNotice(clipsync.Notice{Kind: clipsync.NoticeStatus, Message: "background connection is alive"})
	a.update(func() { a.applyDiscovery(discovery.Snapshot{Message: "background discovery is alive"}) })
	mygo.RunOnMain(func() {
		if a.win != nil || a.status != "background connection is alive" || a.discoveryMessage != "background discovery is alive" || len(a.entries) != 2 {
			t.Error("background updates depended on an open main window")
		}
	})
	a.show(pageHistory)
	mygo.RunOnMain(func() {
		defer func() {
			if a.win != nil {
				a.win.Close()
			}
		}()
		if a.win == nil || a.win == first || a.win.IsDestroyed() || !a.win.IsVisible() {
			t.Error("opening history did not create a visible replacement window")
			return
		}
		if got := a.win.Bounds(); got != bounds {
			t.Errorf("window geometry changed: got %+v, want %+v", got, bounds)
		}
		if _, err := a.win.CapturePage(); err != nil {
			t.Errorf("render rebuilt history: %v", err)
		}
		for range 3 {
			previous := a.win
			previous.Close()
			a.reveal(pageSettings)
			if !previous.IsDestroyed() || a.win == previous || a.page != pageSettings {
				t.Error("repeated close and reopen retained the old window")
			}
		}
	})
}

func TestMainWindowClosedPickerLoadsAndFileRequestsStillWork(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	a := interfaceTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.root = ctx
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("file contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	mygo.RunOnMain(func() {
		a.startTray()
		if a.tray == nil {
			t.Error("could not create the tray")
			return
		}
		a.openPicker(false)
		a.openMainWindow()
		a.win.Close()
		if a.win != nil || a.picker == nil || a.picker.IsDestroyed() {
			t.Error("closing main window affected the independent picker")
		}
		a.thumb("missing thumbnail")
	})
	t.Cleanup(func() {
		mygo.RunOnMain(func() {
			a.closePicker()
			a.releaseThumbnails()
			if a.fileWin != nil {
				a.fileWin.Close()
			}
			if a.tray != nil {
				a.tray.Destroy()
			}
		})
	})
	deadline := time.Now().Add(3 * time.Second)
	loaded := false
	for time.Now().Before(deadline) {
		mygo.RunOnMain(func() { loaded = a.thumbs.Contains("missing thumbnail") && a.thumbActive == 0 })
		if loaded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !loaded {
		t.Fatal("picker's background thumbnail load stalled without the main window")
	}
	mygo.RunOnMain(func() {
		a.openMainWindow()
		a.win.Close()
		if !a.thumbs.Contains("missing thumbnail") {
			t.Error("closing main window released thumbnails still used by the picker")
		}
		a.handleFileRequest([]string{path}, false)
		if a.win != nil || a.pendingFileRequest != nil || a.fileWin == nil || a.filePath != path {
			t.Error("file request waited for the closed main window")
		}
		a.fileWin.Close()
		a.closePicker()
		if a.thumbTimer != nil {
			a.thumbTimer.Stop()
		}
		a.thumbUsedAt = time.Now().Add(-thumbIdle)
		a.scheduleThumbnailTrim(time.Millisecond)
	})
	trimmed := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mygo.RunOnMain(func() { trimmed = a.thumbs.Len() == 0 && a.thumbTimer == nil })
		if trimmed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !trimmed {
		t.Error("idle thumbnail cleanup stalled after all windows closed")
	}
}
