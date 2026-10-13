package clip

import (
	"errors"
	"runtime"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
)

func TestMyGoBoardPropagatesTextWriteFailure(t *testing.T) {
	original := writeClipboardText
	t.Cleanup(func() { writeClipboardText = original })
	nativeErr := errors.New("clipboard is occupied")
	var result error
	var written string
	writeClipboardText = func(data transfer.Data, _ ...mygo.ClipboardOptions) error {
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
