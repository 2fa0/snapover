package sync

import (
	"bytes"
	"crypto/tls"
	"image"
	"image/png"
	"math/rand"
	"net"
	"sync"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/wire"
)

func TestSendQueueByteBudgetAndFIFO(t *testing.T) {
	s := newSession(nil, "peer", false, nil, nil)
	payload := make([]byte, queueBytes/2)
	for i := range 2 {
		if !s.enqueue(clip.Item{Kind: clip.KindImage, PNG: payload, Text: "", SourceApp: string(rune('a' + i)), Thumbnail: []byte("unused"), Preview: []byte("unused preview")}) {
			t.Fatal("valid payload rejected")
		}
	}
	if s.enqueue(clip.Item{Kind: clip.KindText, Text: "extra"}) {
		t.Fatal("byte budget exceeded")
	}
	first := <-s.out
	if first.SourceApp != "a" || len(first.Thumbnail) != 0 || len(first.Preview) != 0 || !s.releaseQueued(first) {
		t.Fatal("FIFO or thumbnail release failed")
	}
	if !s.enqueue(clip.Item{Kind: clip.KindText, Text: "new"}) {
		t.Fatal("dequeue did not release budget")
	}
	second := <-s.out
	s.releaseQueued(second)
	third := <-s.out
	s.releaseQueued(third)
	if second.SourceApp != "b" || third.Text != "new" || s.queuedBytes != 0 {
		t.Fatal("accounting or order changed")
	}
	if s.enqueue(clip.Item{PNG: make([]byte, queueBytes+1)}) {
		t.Fatal("oversize payload queued")
	}
	for range queueSize {
		if !s.enqueue(clip.Item{Text: "small"}) {
			t.Fatal("small queue filled too soon")
		}
	}
	if s.enqueue(clip.Item{Text: "overflow"}) {
		t.Fatal("item count limit exceeded")
	}
}

func TestConcurrentQueueCloseReleasesPayloads(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	s := newSession(tls.Client(a, &tls.Config{}), "peer", false, nil, nil)
	var wg sync.WaitGroup
	payload := make([]byte, 256<<10)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				s.enqueue(clip.Item{PNG: payload})
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case item := <-s.out:
				if !s.releaseQueued(item) {
					return
				}
			case <-s.done:
				return
			}
		}
	}()
	s.Close()
	wg.Wait()
	if s.queuedBytes != 0 || len(s.out) != 0 || s.enqueue(clip.Item{Text: "closed"}) {
		t.Fatal("closed queue retained payloads")
	}
}

func TestImageAssemblerReusesBufferAndValidatesChunks(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	if _, err := rand.New(rand.NewSource(1)).Read(img.Pix); err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromPNG(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	begin := wire.ImageBegin{ItemID: "image", TotalSize: uint64(len(item.PNG)), Chunks: uint32((len(item.PNG) + wire.ChunkSize - 1) / wire.ChunkSize), Hash: item.Hash[:], SourceApp: "Editor"}
	asm, err := newAssembler(begin)
	if err != nil {
		t.Fatal(err)
	}
	if cap(asm.data) != 0 {
		t.Fatal("header allocated the image buffer")
	}
	for start := 0; start < len(item.PNG); start += wire.ChunkSize {
		end := min(start+wire.ChunkSize, len(item.PNG))
		if err := asm.add(wire.ImageChunk{ItemID: begin.ItemID, Index: uint32(start / wire.ChunkSize), Data: item.PNG[start:end]}); err != nil {
			t.Fatal(err)
		}
	}
	storage := asm.data
	back, err := asm.finish()
	if err != nil || back.Hash != item.Hash || back.SourceApp != "Editor" || &back.PNG[0] != &storage[0] {
		t.Fatal("assembly changed content or copied whole PNG")
	}
	bad := begin
	bad.Hash = make([]byte, 32)
	asm, _ = newAssembler(bad)
	for start := 0; start < len(item.PNG); start += wire.ChunkSize {
		asm.add(wire.ImageChunk{ItemID: bad.ItemID, Index: uint32(start / wire.ChunkSize), Data: item.PNG[start:min(start+wire.ChunkSize, len(item.PNG))]})
	}
	if _, err := asm.finish(); err == nil {
		t.Fatal("wrong hash accepted")
	}
	for _, chunk := range []wire.ImageChunk{
		{ItemID: "wrong", Data: []byte("x")}, {ItemID: "image", Index: 1, Data: []byte("x")},
		{ItemID: "image"}, {ItemID: "image", Data: make([]byte, wire.ChunkSize+1)},
	} {
		asm, _ = newAssembler(begin)
		if err := asm.add(chunk); err == nil {
			t.Fatal("invalid chunk accepted")
		}
	}
	for _, bad := range []wire.ImageBegin{
		{}, {TotalSize: clip.MaxImageBytes + 1, Chunks: 1, Hash: make([]byte, 32)},
		{TotalSize: 1, Chunks: 2, Hash: make([]byte, 32)}, {TotalSize: 1, Chunks: 1},
	} {
		if _, err := newAssembler(bad); err == nil {
			t.Fatal("invalid image header accepted")
		}
	}
	asm, _ = newAssembler(begin)
	if _, err := asm.finish(); err == nil {
		t.Fatal("incomplete image accepted")
	}
}
