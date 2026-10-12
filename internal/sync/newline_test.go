package sync

import (
	"testing"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/wire"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
)

func TestTextFromWireNewlines(t *testing.T) {
	for _, text := range []string{"你好\n\n\tworld\n", "你好\r\n\r\n\tworld\r\n"} {
		hash := blake3.Sum256([]byte(text))
		msg := wire.Text{Text: text, Hash: hash[:], SourceApp: "Editor"}
		payload, err := msgpack.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		item, err := textFromWire(payload)
		if err != nil {
			t.Fatal(err)
		}
		const want = "你好\n\n\tworld\n"
		if item.Text != want || item.Hash != clip.HashText(want) || item.Size != len(want) || item.SourceApp != "Editor" {
			t.Fatalf("unexpected received item: %+v", item)
		}
		msg.Text += "tampered"
		payload, err = msgpack.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := textFromWire(payload); err == nil {
			t.Fatal("tampered text passed hash verification")
		}
	}
}
