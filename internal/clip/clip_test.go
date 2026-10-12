package clip

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"sync"
	"testing"
	"time"
)

func TestHashImageIgnoresEncoding(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
	img.SetNRGBA(1, 1, color.NRGBA{R: 9, G: 8, B: 7, A: 128})
	var a, b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&a, img); err != nil {
		t.Fatal(err)
	}
	enc.CompressionLevel = png.BestCompression
	if err := enc.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("expected different PNG encodings")
	}
	ia, err := FromPNG(a.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	ib, err := FromPNG(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if ia.Hash != ib.Hash {
		t.Fatalf("pixel hash changed with encoding")
	}
}

func TestFromTextLimit(t *testing.T) {
	if _, err := FromText(""); err == nil {
		t.Fatal("empty text should fail")
	}
	big := string(bytes.Repeat([]byte("a"), MaxTextBytes+1))
	if _, err := FromText(big); err == nil {
		t.Fatal("oversize text should fail")
	}
	item, err := FromText("你好")
	if err != nil {
		t.Fatal(err)
	}
	if item.Hash != HashText("你好") {
		t.Fatal("hash mismatch")
	}
}

func TestSensitive(t *testing.T) {
	read := func(name string) ([]byte, bool) {
		if name == "CanIncludeInClipboardHistory" {
			return []byte{1, 0, 0, 0}, true
		}
		return nil, false
	}
	if ok, _ := Sensitive([]string{"public.utf8-plain-text"}, read); ok {
		t.Fatal("plain text marked sensitive")
	}
	if ok, why := Sensitive([]string{"org.nspasteboard.ConcealedType"}, read); !ok || why == "" {
		t.Fatal("concealed type not skipped")
	}
	if ok, _ := Sensitive([]string{"CanIncludeInClipboardHistory"}, read); ok {
		t.Fatal("history allowed value 1 should sync")
	}
	readZero := func(string) ([]byte, bool) { return []byte{0, 0, 0, 0}, true }
	if ok, _ := Sensitive([]string{"CanUploadToCloudClipboard"}, readZero); !ok {
		t.Fatal("cloud clipboard 0 should skip")
	}
	readMissing := func(string) ([]byte, bool) { return nil, false }
	if ok, _ := Sensitive([]string{"CanIncludeInClipboardHistory"}, readMissing); !ok {
		t.Fatal("unreadable history flag should fail closed")
	}
}

func TestEchoGuard(t *testing.T) {
	now := time.Unix(0, 0)
	g := NewEchoGuard(5 * time.Second)
	g.now = func() time.Time { return now }
	var h [32]byte
	h[0] = 7
	if g.IsEcho(h) {
		t.Fatal("unmarked hash is echo")
	}
	g.Mark(h)
	if !g.IsEcho(h) {
		t.Fatal("fresh mark should be echo")
	}
	now = now.Add(5 * time.Second)
	if g.IsEcho(h) {
		t.Fatal("expired mark should not be echo")
	}
}

func TestWatchIgnoresStartupContents(t *testing.T) {
	b := &fakeBoard{seq: 3, item: Item{Kind: KindText, Text: "already there"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan Item, 1)
	go Watch(ctx, b, 5*time.Millisecond, func(_ uint64, item Item) {
		got <- item
	})
	select {
	case item := <-got:
		t.Fatalf("startup snapshot was delivered: %+v", item)
	case <-time.After(30 * time.Millisecond):
	}
	b.Set(Item{Kind: KindText, Text: "new"})
	select {
	case item := <-got:
		if item.Text != "new" {
			t.Fatalf("got %q", item.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("change was not delivered")
	}
}

type fakeBoard struct {
	mu     sync.Mutex
	seq    uint64
	item   Item
	writes []Item
	snaps  int
}

func (f *fakeBoard) Sequence() (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seq, nil
}
func (f *fakeBoard) Snapshot() (Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snaps++
	return f.item, nil
}
func (f *fakeBoard) Write(item Item) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.item = item
	f.seq++
	f.writes = append(f.writes, item)
	return nil
}
func (f *fakeBoard) Set(item Item) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.item = item
	f.seq++
}
