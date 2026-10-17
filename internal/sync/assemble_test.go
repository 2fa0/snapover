package sync

import (
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/wire"
)

func TestImageAssemblerReleasesBufferWithoutAnotherFrame(t *testing.T) {
	previous := imageAssembleWait
	imageAssembleWait = 20 * time.Millisecond
	t.Cleanup(func() { imageAssembleWait = previous })

	begin := wire.ImageBegin{ItemID: "img", TotalSize: 4, Chunks: 1, Hash: make([]byte, 32)}
	asm, err := newAssembler(begin)
	if err != nil {
		t.Fatal(err)
	}
	expired := make(chan struct{}, 1)
	asm.arm(func() { expired <- struct{}{} })
	if err := asm.add(wire.ImageChunk{ItemID: begin.ItemID, Data: []byte{1, 2, 3, 4}}); err != nil {
		t.Fatal(err)
	}
	if asm.buffered() == 0 {
		t.Fatal("chunk was not buffered")
	}
	select {
	case <-expired:
	case <-time.After(time.Second):
		t.Fatal("assembly timeout did not fire without another frame")
	}
	if asm.buffered() != 0 || !asm.failedNow() {
		t.Fatalf("buffer=%d failed=%v", asm.buffered(), asm.failedNow())
	}
}

func TestImageChunkExtendsAssemblyDeadline(t *testing.T) {
	previous := imageAssembleWait
	imageAssembleWait = 40 * time.Millisecond
	t.Cleanup(func() { imageAssembleWait = previous })

	begin := wire.ImageBegin{ItemID: "img", TotalSize: 4, Chunks: 1, Hash: make([]byte, 32)}
	asm, err := newAssembler(begin)
	if err != nil {
		t.Fatal(err)
	}
	expired := make(chan struct{}, 1)
	asm.arm(func() { expired <- struct{}{} })
	time.Sleep(20 * time.Millisecond)
	if err := asm.add(wire.ImageChunk{ItemID: begin.ItemID, Data: []byte{1, 2, 3, 4}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-expired:
		t.Fatal("a new image chunk must extend the deadline; a heartbeat would not")
	case <-time.After(25 * time.Millisecond):
	}
	asm.stop()
}
