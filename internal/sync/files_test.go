package sync

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/settings"
	"github.com/2fa0/snapover/internal/wire"
	"github.com/zeebo/blake3"
)

func TestSlowFileReceiveKeepsClipboardAndPingMoving(t *testing.T) {
	a, b := newPair(t)
	dir := t.TempDir()
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	b.mu.Lock()
	receiver := b.sess
	b.mu.Unlock()
	cancelled := make(chan struct{}, 1)
	receiver.mu.Lock()
	receiver.receiveDir = func() string {
		close(started)
		<-release
		return dir
	}
	receiver.onFile = func(n Notice) {
		if n.FileState == "error" {
			cancelled <- struct{}{}
		}
	}
	receiver.mu.Unlock()
	a.mu.Lock()
	sender := a.sess
	a.mu.Unlock()
	if err := sender.write(wire.TypeFileBegin, wire.FileBegin{ItemID: "slow", Name: "slow.bin", TotalSize: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("file worker did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.SendClipboard(ctx, b.LocalID, mustText(t, "while disk is stalled")); err != nil {
		t.Fatalf("file work blocked clipboard delivery or its acknowledgment: %v", err)
	}
	// A round-trip ping exercises readLoop and the response writer while the
	// file worker is still blocked, independently of clipboard processing.
	sender.mu.Lock()
	before := sender.lastRecv
	sender.mu.Unlock()
	if err := sender.write(wire.TypePing, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		sender.mu.Lock()
		defer sender.mu.Unlock()
		return sender.lastRecv.After(before)
	})
	once.Do(func() { close(release) })
	if err := sender.write(wire.TypeFileCancel, wire.ImageEnd{ItemID: "slow"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("file cancellation did not finish")
	}
	waitFor(t, func() bool { entries, _ := os.ReadDir(dir); return len(entries) == 0 })
}

func TestFileQueueBoundsAndCloseReleasesFrames(t *testing.T) {
	left, right := newIdentities(t)
	client, server := handshakePair(t, left, right)
	defer client.NetConn().Close()
	defer server.NetConn().Close()
	s := newSession(client, "peer", true, discardLog(), nil)
	// One default-sized transfer fits even when the receiving disk is stalled.
	frame := make([]byte, wire.ChunkSize+128)
	for range (settings.DefaultMaxFileMB << 20) / wire.ChunkSize {
		if !s.queueFileFrame(wire.TypeFileChunk, frame) {
			t.Fatal("legal transfer does not fit the bounded queue")
		}
	}
	if s.queueFileFrame(wire.TypeFileChunk, make([]byte, wire.ChunkSize+1025)) {
		t.Fatal("oversized file frame accepted")
	}
	for s.queueFileFrame(wire.TypeFileChunk, frame) {
	}
	if s.fileFrameBytes > fileQueueBytes || len(s.fileFrames) > fileQueueSize {
		t.Fatal("file queue exceeded its budget")
	}
	s.Close()
	if len(s.fileFrames) != 0 || s.fileFrameBytes != 0 || s.queueFileFrame(wire.TypeFileChunk, frame) {
		t.Fatal("closed session retained or accepted file frames")
	}
	// The clipboard has a separate queue and byte budget.
	other := newSession(nil, "peer", true, discardLog(), nil)
	for range fileQueueSize {
		other.queueFileFrame(wire.TypeFileBegin, []byte("pending"))
	}
	if !other.enqueue(clip.Item{Kind: clip.KindText, Text: "clipboard"}) {
		t.Fatal("file backlog consumed the clipboard queue")
	}
}

func TestFileExchangeAndClipboardInterleave(t *testing.T) {
	a, b := newPair(t)
	receiveA, receiveB := t.TempDir(), t.TempDir()
	a.SetReceiveDir(receiveA)
	b.SetReceiveDir(receiveB)
	a.SetPolicy(true, true, 1) // Explicit sending is independent of clipboard policy.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "边界.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(settings.DefaultMaxFileMB << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	text := mustText(t, "文件传输期间的剪贴板")
	var once sync.Once
	name, err := a.SendFile(ctx, b.LocalID, path, func(done, total int64) {
		if total != (settings.DefaultMaxFileMB<<20) || done > total {
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
	// Raising the limit on an existing connection allows files above 200 MB.
	const largerSize = (settings.DefaultMaxFileMB + 1) << 20
	if err := os.Truncate(path, largerSize); err != nil {
		t.Fatal(err)
	}
	a.SetFileLimit(largerSize)
	b.SetFileLimit(largerSize)
	if _, err := a.SendFile(ctx, b.LocalID, path, nil); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(receiveB, "边界 (1).bin")); err != nil || info.Size() != largerSize {
		t.Fatalf("larger file: %v, %v", info, err)
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

func TestFileLimitChecksBothPeersAndSnapshotsActiveTransfer(t *testing.T) {
	a, b := newPair(t)
	dir := t.TempDir()
	b.SetReceiveDir(dir)
	if a.FileLimit() != 200<<20 || b.FileLimit() != 200<<20 {
		t.Fatal("unexpected default file limit")
	}
	path := filepath.Join(t.TempDir(), "limited.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(2 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.SetFileLimit(1 << 20)
	if _, err := a.SendFile(ctx, b.LocalID, path, nil); err == nil || !strings.Contains(err.Error(), "1 MB") {
		t.Fatalf("sender did not enforce its limit: %v", err)
	}
	a.SetFileLimit(2 << 20)
	b.SetFileLimit(1 << 20)
	if _, err := a.SendFile(ctx, b.LocalID, path, nil); err == nil || !strings.Contains(err.Error(), "接收上限 1 MB") {
		t.Fatalf("receiver did not enforce its limit: %v", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("rejected transfer left files: %v, %v", entries, err)
	}
	b.SetFileLimit(2 << 20)
	if _, err := a.SendFile(ctx, b.LocalID, path, func(done, total int64) {
		if done == 0 {
			a.SetFileLimit(1 << 20)
			b.SetFileLimit(1 << 20)
		}
	}); err != nil {
		t.Fatalf("limit change interrupted an accepted transfer: %v", err)
	}
	if hashFile(t, filepath.Join(dir, "limited.bin")) != hashFile(t, path) {
		t.Fatal("file content changed")
	}
	if _, err := a.SendFile(ctx, b.LocalID, path, nil); err == nil {
		t.Fatal("new transfer ignored the lowered limit")
	}
	item := mustText(t, "clipboard after limit change")
	if err := a.SendClipboard(ctx, b.LocalID, item); err != nil {
		t.Fatalf("file rejection broke clipboard delivery: %v", err)
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
