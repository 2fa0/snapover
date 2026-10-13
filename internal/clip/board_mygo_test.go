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
