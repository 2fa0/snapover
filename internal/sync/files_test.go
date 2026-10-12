package sync

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/filetransfer"
	"github.com/2fa0/snapover/internal/wire"
	"github.com/zeebo/blake3"
)

func TestFileExchangeAndClipboardInterleave(t *testing.T) {
	a, b := newPair(t)
	receiveA, receiveB := t.TempDir(), t.TempDir()
	a.SetReceiveDir(receiveA)
	b.SetReceiveDir(receiveB)
	a.SetPolicy(true, true, 1) // Explicit sending is independent of clipboard policy.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "边界.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(filetransfer.MaxBytes); err != nil {
		t.Fatal(err)
	}
	f.Close()
	text := mustText(t, "文件传输期间的剪贴板")
	var once sync.Once
	name, err := a.SendFile(ctx, b.LocalID, path, func(done, total int64) {
		if total != filetransfer.MaxBytes || done > total {
			t.Errorf("invalid progress %d/%d", done, total)
		}
		if done > 0 {
			once.Do(func() {
				a.SetPolicy(false, false, 0)
				a.HandleLocal(99, text)
			})
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if name != filepath.Base(path) {
		t.Fatal(name)
	}
	waitWrite(t, b.Board.(*memBoard), text.Hash)
	received := filepath.Join(receiveB, name)
	if hashFile(t, received) != hashFile(t, path) {
		t.Fatal("file content changed")
	}
	if _, err := a.SendFile(ctx, b.LocalID, path, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(receiveB, "边界 (1).bin")); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendFile(ctx, a.LocalID, empty, nil); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(receiveA, "empty")); err != nil || info.Size() != 0 {
		t.Fatalf("%v %v", info, err)
	}
	if writes := len(a.Board.(*memBoard).Writes()); writes != 0 {
		t.Fatalf("file changed clipboard: %d", writes)
	}
}

func TestFileRejectionKeepsClipboardConnection(t *testing.T) {
	a, b := newPair(t)
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	b.SetReceiveDir(blocked)
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := a.SendFile(ctx, b.LocalID, path, nil); err == nil {
		t.Fatal("accepted unwritable destination")
	}
	item := mustText(t, "still connected")
	a.HandleLocal(1, item)
	waitWrite(t, b.Board.(*memBoard), item.Hash)
}

func TestSourceMutationCancelsAndCleansReceiver(t *testing.T) {
	a, b := newPair(t)
	dir := t.TempDir()
	b.SetReceiveDir(dir)
	path := filepath.Join(t.TempDir(), "changing")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := a.SendFile(ctx, b.LocalID, path, func(done, total int64) {
		if done == 0 {
			if err := os.Truncate(path, 0); err != nil {
				t.Error(err)
			}
		}
	})
	if err == nil {
		t.Fatal("sent modified source")
	}
	waitFor(t, func() bool { entries, _ := os.ReadDir(dir); return len(entries) == 0 })
	if !a.Connected() || !b.Connected() {
		t.Fatal("source mutation broke clipboard connection")
	}
}

func TestSimultaneousFileTransfers(t *testing.T) {
	a, b := newPair(t)
	dirA, dirB := t.TempDir(), t.TempDir()
	a.SetReceiveDir(dirA)
	b.SetReceiveDir(dirB)
	path := filepath.Join(t.TempDir(), "both")
	if err := os.WriteFile(path, make([]byte, 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	errors := make(chan error, 2)
	go func() { _, err := a.SendFile(ctx, b.LocalID, path, nil); errors <- err }()
	go func() { _, err := b.SendFile(ctx, a.LocalID, path, nil); errors <- err }()
	for i := 0; i < 2; i++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{dirA, dirB} {
		if hashFile(t, filepath.Join(dir, "both")) != hashFile(t, path) {
			t.Fatal("content mismatch")
		}
	}
}

func TestFileDisconnectRemovesPartial(t *testing.T) {
	a, b := newPair(t)
	dir := t.TempDir()
	b.SetReceiveDir(dir)
	a.mu.Lock()
	s := a.sess
	a.mu.Unlock()
	if err := s.write(wire.TypeFileBegin, wire.FileBegin{ItemID: "interrupted", Name: "file", TotalSize: 4}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { entries, _ := os.ReadDir(dir); return len(entries) == 1 })
	s.Close()
	waitFor(t, func() bool { entries, _ := os.ReadDir(dir); return len(entries) == 0 })
}

func TestFileCapabilityAndDisconnectedErrors(t *testing.T) {
	e := testEngine(t, &memBoard{})
	if _, err := e.SendFile(context.Background(), "", "unused", nil); err == nil {
		t.Fatal("sent while disconnected")
	}
	e.sess = &Session{}
	if _, err := e.SendFile(context.Background(), "", "unused", nil); err == nil {
		t.Fatal("sent to old peer")
	}
}

func hashFile(t *testing.T, path string) [32]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := blake3.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	var hash [32]byte
	copy(hash[:], h.Sum(nil))
	return hash
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition timed out")
}
