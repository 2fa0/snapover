package clip

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"runtime"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
)

func TestMyGoBoardPropagatesTextWriteFailure(t *testing.T) {
	stubClipboardThread(t)
	original := writeClipboardData
	t.Cleanup(func() { writeClipboardData = original })
	nativeErr := errors.New("clipboard is occupied")
	var result error
	var written string
	writeClipboardData = func(data transfer.Data, _ ...mygo.ClipboardOptions) error {
		raw, err := data.Read(transfer.Text)
		if err != nil {
			t.Fatal(err)
		}
		written = string(raw)
		return result
	}
	item, err := FromText("  中文\r\n\n末尾\n")
	if err != nil {
		t.Fatal(err)
	}
	board := MyGoBoard{}
	result = nativeErr
	if err := board.Write(item); !errors.Is(err, nativeErr) {
		t.Fatalf("native failure was swallowed: %v", err)
	}
	if written != clipboardText(item.Text, runtime.GOOS) {
		t.Fatalf("text changed: %q", written)
	}
	result = nil
	if err := board.Write(item); err != nil {
		t.Fatal(err)
	}
}

func TestMyGoBoardPublishesOriginalPNGAndPropagatesFailure(t *testing.T) {
	stubClipboardThread(t)
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	item, err := PNGForPaste(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	original := writeClipboardData
	t.Cleanup(func() { writeClipboardData = original })
	nativeErr := errors.New("clipboard is occupied")
	writeClipboardData = func(data transfer.Data, _ ...mygo.ClipboardOptions) error {
		png, err := data.Read(transfer.PNG)
		if err != nil || !bytes.Equal(png, raw.Bytes()) {
			t.Fatal("image write changed the original PNG")
		}
		return nativeErr
	}
	if err := (MyGoBoard{}).Write(item); !errors.Is(err, nativeErr) {
		t.Fatalf("native image write failure was swallowed: %v", err)
	}
}

func stubClipboardThread(t *testing.T) {
	t.Helper()
	originalRun, originalFlush := runOnClipboardThread, flushClipboardData
	originalPrepare := prepareClipboardImage
	t.Cleanup(func() {
		runOnClipboardThread, flushClipboardData = originalRun, originalFlush
		prepareClipboardImage = originalPrepare
	})
	runOnClipboardThread = func(fn func()) { fn() }
	flushClipboardData = func() error { return nil }
	prepareClipboardImage = func(Item, bool) (PreparedWrite, bool, error) { return PreparedWrite{}, false, nil }
}

func TestLocalWritesCarryDistinctMarkerWithoutBecomingRemoteEchoes(t *testing.T) {
	stubClipboardThread(t)
	original := writeClipboardData
	t.Cleanup(func() { writeClipboardData = original })
	item, err := FromText("history content")
	if err != nil {
		t.Fatal(err)
	}
	for _, remote := range []bool{false, true} {
		writeClipboardData = func(data transfer.Data, _ ...mygo.ClipboardOptions) error {
			format, other := localFormat, remoteFormat
			if remote {
				format, other = remoteFormat, localFormat
			}
			marker, err := data.Read(format)
			if err != nil || !bytes.Equal(marker, remoteMarker) {
				t.Fatal("missing write-origin marker", err)
			}
			if raw, _ := data.Read(other); len(raw) != 0 {
				t.Fatal("local write was classified as a remote echo")
			}
			return nil
		}
		prepared, err := (MyGoBoard{}).prepareWrite(item, remote)
		if err != nil {
			t.Fatal(err)
		}
		if err := prepared.Publish(); err != nil {
			t.Fatal(err)
		}
		prepared.Close()
	}
}

func TestMyGoBoardPersistsWritesBeforeReturning(t *testing.T) {
	text, err := FromText("persist this text")
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	img, err := PNGForPaste(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	nativeErr := errors.New("clipboard is occupied")
	for _, item := range []Item{text, img} {
		for _, outcome := range []string{"success", "write-error", "flush-error"} {
			t.Run(string(item.Kind)+"/"+outcome, func(t *testing.T) {
				stubClipboardThread(t)
				originalWrite := writeClipboardData
				t.Cleanup(func() { writeClipboardData = originalWrite })
				inside := false
				callbacks, writes, flushes := 0, 0, 0
				runOnClipboardThread = func(fn func()) {
					callbacks++
					inside = true
					fn()
					inside = false
				}
				writeClipboardData = func(transfer.Data, ...mygo.ClipboardOptions) error {
					if !inside {
						t.Fatal("write escaped the owning thread")
					}
					writes++
					if outcome == "write-error" {
						return nativeErr
					}
					return nil
				}
				flushClipboardData = func() error {
					if !inside || writes != 1 {
						t.Fatal("flush must follow the write in the same owning-thread callback")
					}
					flushes++
					if outcome == "flush-error" {
						return nativeErr
					}
					return nil
				}
				err := (MyGoBoard{}).Write(item)
				if outcome == "success" && err != nil || outcome != "success" && !errors.Is(err, nativeErr) {
					t.Fatalf("write/flush outcome was lost: %v", err)
				}
				wantFlushes := 1
				if outcome == "write-error" {
					wantFlushes = 0
				}
				if callbacks != 1 || writes != 1 || flushes != wantFlushes {
					t.Fatalf("callbacks=%d writes=%d flushes=%d, want 1,1,%d", callbacks, writes, flushes, wantFlushes)
				}
			})
		}
	}
}
