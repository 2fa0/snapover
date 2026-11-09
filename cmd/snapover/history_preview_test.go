package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestMainHistoryHoverUsesRowBoundsAndSkipsShortText(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry history.Entry
		want  bool
	}{
		{"short", history.Entry{ID: "short", Kind: "text", Text: "短文本"}, false},
		{"long", history.Entry{ID: "long", Kind: "text", Text: strings.Repeat("文", 100)}, true},
		{"image", history.Entry{ID: "image", Kind: "image", Width: 20, Height: 10}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := interfaceTestApp(t)
			a.page, a.entries = pageHistory, []history.Entry{tc.entry}
			// Exercise hover routing without creating a native preview window.
			a.win, a.pickLoading = new(mygo.Window), true
			tt := ui.NewTester(a.view, 960, 640)
			tt.SetPreferences(ui.Preferences{ReduceMotion: true})
			bounds, ok := tt.Find(entryTitle(tc.entry))
			if !ok {
				t.Fatal("history title not rendered")
			}
			tt.Move(bounds.X+bounds.W/2, bounds.Y+bounds.H/2)
			if got := a.pickPreview.hover.id != ""; got != tc.want {
				t.Fatalf("hover preview = %v, want %v; bounds=%+v preview=%+v", got, tc.want, bounds, a.pickPreview)
			}
			if tc.want {
				anchor := a.pickPreview.anchor
				if a.pickPreview.parent != a.win || anchor.X <= 184 || anchor.Width >= 960 || anchor.Height <= 0 {
					t.Fatalf("preview not anchored to the main history row: %+v", anchor)
				}
				clickInterface(t, tt, "设置")
				if a.pickPreview.parent != nil || a.pickPreview.hover.id != "" {
					t.Fatal("leaving history retained its preview")
				}
			}
		})
	}
}

func TestMainHistoryPreviewLoadsAndKeepsMainWindow(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	for _, kind := range []string{"text", "image"} {
		t.Run(kind, func(t *testing.T) {
			a := interfaceTestApp(t)
			body := strings.Repeat("  长文本正文\n", 2000) + "完整尾部"
			item, _ := clip.FromText(body)
			if kind == "image" {
				var raw bytes.Buffer
				if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 8, 4))); err != nil {
					t.Fatal(err)
				}
				var err error
				item, err = clip.FromPNG(raw.Bytes())
				if err != nil {
					t.Fatal(err)
				}
			}
			entry, err := a.hist.Add(history.DirLocal, "", item)
			if err != nil {
				t.Fatal(err)
			}
			var parent *mygo.Window
			mygo.RunOnMain(func() {
				a.refreshEntries()
				a.page = pageHistory
				a.openMainWindow()
				parent = a.win
				parent.Hide()
			})
			t.Cleanup(func() {
				mygo.RunOnMain(func() {
					a.closePickPreview()
					if a.win != nil {
						a.win.Close()
					}
					if a.pickOriginal != nil {
						a.pickOriginal.Close()
					}
					a.releaseThumbnails()
				})
			})
			startPreview := func() {
				a.pickPreview.hover = previewHover{id: entry.ID, since: time.Now().Add(-pickPreviewDelay)}
				a.pickPreview.anchor = mygo.Rectangle{X: 212, Y: 180, Width: 600, Height: 60}
				a.loadPickPreview(entry.ID, parent)
			}
			mygo.RunOnMain(startPreview)
			waitForImageOpen(t, a)
			mygo.RunOnMain(func() {
				if a.pickPreview.win == nil || a.pickPreview.parent != parent {
					t.Error("main history preview did not open")
					return
				}
				if _, err := a.pickPreview.win.CapturePage(); err != nil {
					t.Errorf("render main history preview: %v", err)
				}
			})
			mygo.RunOnMain(func() {
				if kind == "text" {
					a.loadPickText(entry.ID, parent)
				} else {
					a.openImageViewer(entry)
				}
			})
			if kind == "text" {
				waitForImageOpen(t, a)
			} else {
				waitForViewer(t, func() bool {
					return a.imageViewer != nil && (a.imageViewer.err != "" || a.imageViewer.request == nil)
				})
			}
			mygo.RunOnMain(func() {
				if a.win != parent || parent.IsDestroyed() || a.pickPreview.win != nil {
					t.Error("opening full content closed the main window or retained the preview")
				}
				if kind == "text" {
					if a.pickOriginal == nil {
						t.Error("full text did not open")
					} else if _, err := a.pickOriginal.CapturePage(); err != nil {
						t.Errorf("render full text: %v", err)
					}
				} else if viewer := a.imageViewer; viewer == nil || viewer.win == nil {
					t.Error("the image viewer did not open")
				} else if _, err := viewer.win.CapturePage(); err != nil {
					t.Errorf("render the image viewer: %v", err)
				}
				startPreview()
				parent.Close()
			})
			waitForImageOpen(t, a)
			mygo.RunOnMain(func() {
				if a.win != nil || a.pickPreview.win != nil || a.pickPreview.request != nil {
					t.Error("late load reopened a preview after the main window closed")
				}
				a.closeImageViewer()
			})
		})
	}
}
