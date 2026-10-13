package wire

import (
	"bytes"
	"encoding/binary"
	"io"
	"runtime"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
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

func TestClipboardAckCompatibility(t *testing.T) {
	type legacyAck struct {
		ItemID string `msgpack:"item_id"`
	}
	oldPayload, err := msgpack.Marshal(legacyAck{ItemID: "old"})
	if err != nil {
		t.Fatal(err)
	}
	var current Ack
	if err := Unmarshal(oldPayload, &current); err != nil || current.ItemID != "old" || current.State != "" {
		t.Fatalf("legacy ack: %+v %v", current, err)
	}
	newPayload, err := msgpack.Marshal(Ack{ItemID: "new", State: "error", Message: "已暂停"})
	if err != nil {
		t.Fatal(err)
	}
	var old legacyAck
	if err := Unmarshal(newPayload, &old); err != nil || old.ItemID != "new" {
		t.Fatalf("new ack broke legacy decoding: %+v %v", old, err)
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

func TestRejectDeepUnknownField(t *testing.T) {
	// An unpaired peer can send unknown Hello fields before authentication.
	// A short frame must not drive the decoder into unbounded recursion.
	payload := append([]byte{0x81, 0xa1, 'x'}, bytes.Repeat([]byte{0x91}, 10000)...)
	payload = append(payload, 0xc0)
	var hello Hello
	if err := Unmarshal(payload, &hello); err == nil {
		t.Fatal("accepted deeply nested unknown Hello field")
	}
}

func TestRejectMalformedMessageLengths(t *testing.T) {
	for _, value := range [][]byte{
		{0xdb, 0xff, 0xff, 0xff, 0xff},    // string32
		{0xc6, 0xff, 0xff, 0xff, 0xff},    // binary32
		{0xdd, 0xff, 0xff, 0xff, 0xff},    // array32
		{0xdf, 0xff, 0xff, 0xff, 0xff},    // map32
		{0xc9, 0xff, 0xff, 0xff, 0xff, 1}, // extension32
		{0xdc, 0, 2, 0xc0},                // truncated array16
		{0xde, 0, 1, 0xc0},                // truncated map16
		{0x81, 0xa1, 'x', 0xc0, 0xc0},     // trailing value
	} {
		payload := append([]byte{0x81, 0xa1, 'x'}, value...)
		var hello Hello
		if err := Unmarshal(payload, &hello); err == nil {
			t.Fatalf("accepted malformed unknown field: %x", value)
		}
	}
}

func TestRejectTruncatedBinaryBeforeAllocating(t *testing.T) {
	// A declared 16 MB hash is carried in a tiny frame. Decoding must
	// reject it before allocating a buffer based on that untrusted length.
	payload := []byte{0x81, 0xa4, 'h', 'a', 's', 'h', 0xc6, 1, 0, 0, 0}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var text Text
	err := Unmarshal(payload, &text)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("accepted truncated binary field")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
		t.Fatalf("allocated %d bytes for a truncated binary field", allocated)
	}
}

func TestUnknownFieldsRemainCompatible(t *testing.T) {
	payload, err := msgpack.Marshal(map[string]any{
		"device_id": "device", "proto": Proto,
		"future": map[string]any{"values": []any{true, nil, 42, "text", []byte{1, 2}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var hello Hello
	if err := Unmarshal(payload, &hello); err != nil || hello.DeviceID != "device" || hello.Proto != Proto {
		t.Fatalf("hello=%+v err=%v", hello, err)
	}
}

func FuzzUnmarshalHello(f *testing.F) {
	payload, err := msgpack.Marshal(Hello{DeviceID: "device", Proto: Proto, Capabilities: []string{FileCapability, PairCapability}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(payload)
	f.Add([]byte{0x81, 0xa1, 'x', 0x91, 0xc0})
	f.Add([]byte{0x81, 0xa1, 'x', 0xc6, 0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, payload []byte) {
		var hello Hello
		_ = Unmarshal(payload, &hello)
	})
}
