package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo"
)

// imageViewerTestApp stores one image and returns the viewer state that would
// show it, without opening a window: the system handoff does not need one.
func imageViewerTestApp(t *testing.T) (*app, *imageViewerState) {
	t.Helper()
	a := interfaceTestApp(t)
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromPNG(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := a.hist.Add(history.DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	v := newImageViewer(a, entry.ID)
	t.Cleanup(v.release)
	return a, v
}

// waitForImageOpen waits for the picker's preview load to finish.
func waitForImageOpen(t *testing.T, a *app) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var loading bool
		mygo.RunOnMain(func() { loading = a.pickLoading })
		if !loading {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("image opening did not finish")
}

func waitForViewer(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var ok bool
		mygo.RunOnMain(func() { ok = condition() })
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the viewer did not settle")
}

// The handoff stages the original and gives the system viewer a deadline,
// which is what the old preview's launch did before the picture had a window
// of its own.
func TestViewerHandsTheOriginalToTheSystemViewer(t *testing.T) {
	_, v := imageViewerTestApp(t)
	var path string
	original := openSystemImage
	openSystemImage = func(ctx context.Context, staged string) error {
		if _, err := os.Stat(staged); err != nil {
			t.Errorf("the viewer received a missing copy: %v", err)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("the launch has no deadline")
		}
		path = staged
		return nil
	}
	t.Cleanup(func() { openSystemImage = original })
	if err := v.stageAndOpenSystem(); err != nil {
		t.Fatalf("staging and opening failed: %v", err)
	}
	if path == "" {
		t.Fatal("the original never reached the system viewer")
	}
	t.Cleanup(func() { _ = os.Remove(path) })
}

func TestViewerReportsAFailedSystemOpen(t *testing.T) {
	_, v := imageViewerTestApp(t)
	original := openSystemImage
	openSystemImage = func(context.Context, string) error { return errors.New("viewer unavailable") }
	t.Cleanup(func() { openSystemImage = original })
	request := &struct{}{}
	v.openRequest, v.opening = request, true
	v.finishSystemOpen(request, v.stageAndOpenSystem())
	if v.opening {
		t.Fatal("a finished launch still counts as pending")
	}
	if v.note != "用系统应用打开失败，请重试" {
		t.Fatalf("a failed launch reported %q", v.note)
	}
}

func TestViewerDoesNotLaunchAfterItCloses(t *testing.T) {
	_, v := imageViewerTestApp(t)
	v.release()
	v.openInSystem()
	if v.opening {
		t.Fatal("a closed viewer started a launch")
	}
	if v.openRequest != nil {
		t.Fatal("a closed viewer kept a launch request")
	}
	// A worker queued before Close may not start until after it.
	if err := v.stageAndOpenSystem(); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed viewer staged and launched an original: %v", err)
	}
}

func TestViewerCloseCancelsSystemOpenInProgress(t *testing.T) {
	_, v := imageViewerTestApp(t)
	original := openSystemImage
	t.Cleanup(func() { openSystemImage = original })
	started := make(chan struct{})
	openSystemImage = func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- v.stageAndOpenSystem() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("system launch never started")
	}
	v.release()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("launch was not canceled: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing did not cancel the system launch")
	}
}

func TestViewerReplacementWaitsForCanceledLoad(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	a, old := imageViewerTestApp(t)
	var next *imageViewerState
	mygo.RunOnMain(func() {
		// Hold the decode slot while a closed viewer and its replacement queue.
		a.viewerWork = make(chan struct{}, 1)
		a.viewerWork <- struct{}{}
		a.loadViewerImage(old)
		old.release()
		next = newImageViewer(a, old.id)
		a.loadViewerImage(next)
		<-a.viewerWork
	})
	t.Cleanup(func() { mygo.RunOnMain(next.release) })
	waitForViewer(t, func() bool { return next.request == nil })
	mygo.RunOnMain(func() {
		if next.bmp == nil || next.err != "" {
			t.Errorf("replacement failed to load: %s", next.err)
		}
		if old.bmp != nil || old.request != nil || old.ctx.Err() == nil {
			t.Error("closed viewer accepted work after its replacement loaded")
		}
	})
}

// A result for a launch the window has moved on from changes nothing.
func TestViewerIgnoresAStaleSystemOpen(t *testing.T) {
	_, v := imageViewerTestApp(t)
	current := &struct{}{}
	v.openRequest, v.opening = current, true
	v.finishSystemOpen(&struct{}{}, errors.New("viewer unavailable"))
	if !v.opening || v.note != "" || v.openRequest != current {
		t.Fatal("a stale launch result ended the current one or reported it")
	}
}
