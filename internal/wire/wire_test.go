package wire

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	msg := Text{ItemID: "01", Hash: bytes.Repeat([]byte{1}, 32), Text: "你好", CreatedAt: 9}
	if err := WriteMsg(&buf, TypeText, msg); err != nil {
		t.Fatal(err)
	}
	typ, payload, err := ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if typ != TypeText {
		t.Fatalf("type %d", typ)
	}
	var got Text
	if err := Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.Text != "你好" || got.ItemID != "01" || !bytes.Equal(got.Hash, msg.Hash) {
		t.Fatalf("%+v", got)
	}
}

func TestRejectHugeFrame(t *testing.T) {
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[:4], uint32(MaxFrame+1))
	hdr[4] = TypePing
	_, _, err := ReadFrame(bytes.NewReader(hdr[:]))
	if err == nil {
		t.Fatal("expected length error")
	}
}

func TestShortRead(t *testing.T) {
	_, _, err := ReadFrame(bytes.NewReader([]byte{0, 0, 0, 2, TypePing}))
	if err == nil || err == io.EOF {
		t.Fatalf("err = %v", err)
	}
}

func TestHelloListenPortCompatibility(t *testing.T) {
	for _, port := range []string{"", "49000"} {
		var buf bytes.Buffer
		if err := WriteMsg(&buf, TypeHello, Hello{DeviceID: "device", Proto: Proto, ListenPort: port}); err != nil {
			t.Fatal(err)
		}
		_, payload, err := ReadFrame(&buf)
		if err != nil {
			t.Fatal(err)
		}
		var got Hello
		if err := Unmarshal(payload, &got); err != nil || got.ListenPort != port {
			t.Fatalf("hello=%+v err=%v", got, err)
		}
		var old struct {
			DeviceID string `msgpack:"device_id"`
			Proto    uint16 `msgpack:"proto"`
		}
		if err := Unmarshal(payload, &old); err != nil || old.DeviceID != "device" || old.Proto != Proto {
			t.Fatalf("older hello=%+v err=%v", old, err)
		}
	}
}
