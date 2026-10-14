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

	"github.com/egoist/mygo"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
)

func imageOpenTestApp(t *testing.T) (*app, string, *mygo.Window) {
	t.Helper()
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	a := interfaceTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	a.root = ctx
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
	var parent *mygo.Window
	mygo.RunOnMain(func() {
		parent = mygo.NewWindow(mygo.WindowOptions{Title: "Image opening test", Width: 200, Height: 100, Hidden: true})
		a.picker = parent
		a.pickPreview.hover.id = entry.ID
		parent.OnClosed(func() {
			if a.picker == parent {
				a.picker = nil
			}
		})
	})
	t.Cleanup(func() {
		cancel()
		mygo.RunOnMain(func() {
			a.closePicker()
			parent.Close()
		})
	})
	return a, entry.ID, parent
}

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

func TestPickerImageOpeningKeepsUIResponsive(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "dismiss", "quit"} {
		t.Run(outcome, func(t *testing.T) {
			a, id, parent := imageOpenTestApp(t)
			ctx, cancel := context.WithCancel(a.root)
			defer cancel()
			a.root = ctx
			started, resume := make(chan struct{}), make(chan struct{}, 1)
			defer close(resume)
			original := openSystemImage
			openSystemImage = func(ctx context.Context, path string) error {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("viewer received a missing copy: %v", err)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Error("viewer launch has no deadline")
				}
				close(started)
				select {
				case <-resume:
				case <-ctx.Done():
					return ctx.Err()
				}
				if outcome == "failure" {
					return errors.New("viewer unavailable")
				}
				return nil
			}
			t.Cleanup(func() { openSystemImage = original })
			mygo.RunOnMain(func() { a.loadPickImage(id, parent) })
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("viewer did not start")
			}
			responsive := make(chan struct{})
			go func() {
				mygo.RunOnMain(func() {
					if !a.pickLoading {
						t.Error("opening stopped tracking the pending launch")
					}
					if outcome == "dismiss" {
						a.closePicker()
						a.picker = mygo.NewWindow(mygo.WindowOptions{Title: "Replacement picker", Width: 200, Height: 100, Hidden: true})
					}
					if outcome == "quit" {
						a.quitting = true
						cancel()
						a.hist.CleanOpened()
					}
				})
				close(responsive)
			}()
			select {
			case <-responsive:
			case <-time.After(time.Second):
				t.Fatal("viewer launch blocked the UI thread")
			}
			// Let the viewer return before checking success and error handling.
			if outcome != "quit" {
				resume <- struct{}{}
			}
			waitForImageOpen(t, a)
			mygo.RunOnMain(func() {
				switch outcome {
				case "success":
					if a.picker != nil || !parent.IsDestroyed() {
						t.Error("successful opening did not close the picker")
					}
				case "failure":
					if a.picker != parent || a.pickFlash == "" {
						t.Error("failed opening did not keep the picker and show an error")
					}
				case "dismiss":
					if a.picker == nil || a.picker.IsDestroyed() || a.pickFlash != "" {
						t.Error("late opening affected the replacement picker")
					}
				case "quit":
					if a.pickFlash != "" || a.picker != parent || parent.IsDestroyed() {
						t.Error("canceled opening changed the UI during quit")
					}
				}
			})
		})
	}
}

func TestPickerImageDismissedBeforeLoadDoesNotLaunchViewer(t *testing.T) {
	a, id, parent := imageOpenTestApp(t)
	opened := make(chan struct{}, 1)
	original := openSystemImage
	openSystemImage = func(context.Context, string) error {
		opened <- struct{}{}
		return nil
	}
	t.Cleanup(func() { openSystemImage = original })
	mygo.RunOnMain(func() {
		a.loadPickImage(id, parent)
		a.closePicker()
	})
	waitForImageOpen(t, a)
	select {
	case <-opened:
		t.Fatal("dismissed picker launched the viewer")
	default:
	}
}
