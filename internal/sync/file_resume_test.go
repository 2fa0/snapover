package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/filetransfer"
	"github.com/2fa0/snapover/internal/wire"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
)

func TestControlledPauseResumeAndCancel(t *testing.T) {
	a, b := newPair(t)
	dir := t.TempDir()
	b.SetReceiveDir(dir)
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, make([]byte, 4<<20), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	control := NewFileControl()
	control.Pause(true)
	var done atomic.Int64
	ready := make(chan struct{}, 1)
	result := make(chan error, 1)
	go func() {
		_, err := a.SendFileControlled(ctx, b.LocalID, path, control, func(n, total int64) {
			done.Store(n)
			select {
			case ready <- struct{}{}:
			default:
			}
		}, nil)
		result <- err
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	time.Sleep(150 * time.Millisecond)
	if done.Load() != 0 {
		t.Fatal("paused transfer sent bytes")
	}
	control.Pause(false)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if hashFile(t, path) != hashFile(t, filepath.Join(dir, "payload.bin")) {
		t.Fatal("content mismatch")
	}
	control = NewFileControl()
	control.Pause(true)
	cancelCtx, stop := context.WithCancel(ctx)
	started := make(chan struct{}, 1)
	go func() {
		_, err := a.SendFileControlled(cancelCtx, b.LocalID, path, control, func(n, total int64) {
			select {
			case started <- struct{}{}:
			default:
			}
		}, nil)
		result <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stop()
	if err := <-result; err == nil {
		t.Fatal("cancel succeeded")
	}
	waitFor(t, func() bool { entries, _ := os.ReadDir(dir); return len(entries) == 1 })
}

func TestResumeStoreIsolationAndAcknowledgedOffset(t *testing.T) {
	var store resumeFiles
	defer store.close()
	dir := t.TempDir()
	incoming, err := filetransfer.Begin(dir, "test.bin", 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = incoming.Append(0, []byte("ab")); err != nil {
		t.Fatal(err)
	}
	partial := &fileReceive{id: "token", name: "test.bin", file: incoming, resumable: true}
	store.put("device:cert", partial, "")
	if f, _ := store.take("other:cert", "token"); f != nil {
		t.Fatal("cross-device resume")
	}
	f, _ := store.take("device:cert", "token")
	if f == nil || f.file.Written != 2 {
		t.Fatal("offset lost")
	}
	if err = f.file.Append(2, []byte("cd")); err != nil {
		t.Fatal(err)
	}
	f.abort()
}

func TestResumeCapabilityAdvertised(t *testing.T) {
	a, b := newPair(t)
	if !a.sess.fileResumeSupported || !b.sess.fileResumeSupported {
		t.Fatal(wire.FileResumeCapability)
	}
}

func TestControlledReconnectResumesFromReceiverOffset(t *testing.T) {
	left, right := newIdentities(t)
	a, b := engineFor(t, left, &memBoard{}), engineFor(t, right, &memBoard{})
	a.SetPairing(true)
	b.SetPairing(true)
	attach := func() {
		client, server := handshakePair(t, left, right)
		go a.Attach(server, false)
		go b.Attach(client, true)
	}
	attach()
	waitFor(t, func() bool { approvePair(t, a, b); return a.Connected() && b.Connected() })
	defer a.Stop()
	defer b.Stop()
	dir := t.TempDir()
	b.SetReceiveDir(dir)
	path := filepath.Join(t.TempDir(), "resume.bin")
	if err := os.WriteFile(path, make([]byte, 4<<20), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	control := NewFileControl()
	disconnected := false
	reattached := false
	var resumeOffset int64
	_, err := a.SendFileControlled(ctx, b.LocalID, path, control, func(n, total int64) {
		if !disconnected && n > 0 {
			disconnected = true
			a.Disconnect()
		} else if reattached && resumeOffset == 0 {
			resumeOffset = n
		}
	}, func() {
		if !reattached {
			reattached = true
			attach()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumeOffset == 0 {
		t.Fatal("transfer restarted from zero")
	}
	if hashFile(t, path) != hashFile(t, filepath.Join(dir, "resume.bin")) {
		t.Fatal("resumed content mismatch")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("partial or duplicate files: %v", entries)
	}
}

func TestChangedSourceWhileReconnectingReportsUnknownResult(t *testing.T) {
	left, right := newIdentities(t)
	a, b := engineFor(t, left, &memBoard{}), engineFor(t, right, &memBoard{})
	a.SetPairing(true)
	b.SetPairing(true)
	attach := func() {
		client, server := handshakePair(t, left, right)
		go a.Attach(server, false)
		go b.Attach(client, true)
	}
	attach()
	waitFor(t, func() bool { approvePair(t, a, b); return a.Connected() && b.Connected() })
	defer a.Stop()
	defer b.Stop()
	b.SetReceiveDir(t.TempDir())
	path := filepath.Join(t.TempDir(), "changed.bin")
	if err := os.WriteFile(path, make([]byte, 4<<20), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	disconnected, reattached := false, false
	_, err := a.SendFileControlled(ctx, b.LocalID, path, NewFileControl(), func(done, _ int64) {
		if done > 0 && !disconnected {
			disconnected = true
			a.Disconnect()
		}
	}, func() {
		if !reattached {
			reattached = true
			if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			attach()
		}
	})
	if err != ErrFileOutcomeUnknown {
		t.Fatalf("changed source reported a certain outcome: %v", err)
	}
}

func TestFileSendPipelinesBoundedChunks(t *testing.T) {
	a, b := newPair(t)
	b.SetReceiveDir(t.TempDir())
	path := filepath.Join(t.TempDir(), "pipeline.bin")
	if err := os.WriteFile(path, make([]byte, 2*fileSendWindow*wire.ChunkSize), 0600); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	receiver := b.sess
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observed := make(chan int, 1)
	_, err := a.SendFileControlled(ctx, b.LocalID, path, NewFileControl(), func(done, total int64) {
		if done != 0 {
			return
		}
		receiver.writeMu.Lock()
		go func() {
			defer receiver.writeMu.Unlock()
			for len(receiver.responses) < fileSendWindow-1 && ctx.Err() == nil {
				time.Sleep(time.Millisecond)
			}
			time.Sleep(20 * time.Millisecond)
			observed <- len(receiver.responses)
		}()
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := <-observed; n < fileSendWindow-1 || n > fileSendWindow {
		t.Fatalf("unexpected acknowledgment backlog while writes were blocked: %d", n)
	}
}

func TestCancelAfterReceiverSavedReportsCompletion(t *testing.T) {
	a, b := newPair(t)
	dir := t.TempDir()
	b.SetReceiveDir(dir)
	path := filepath.Join(t.TempDir(), "saved.bin")
	if err := os.WriteFile(path, []byte("already saved"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b.mu.Lock()
	receiver := b.sess
	b.mu.Unlock()
	var locked atomic.Bool
	var release sync.Once
	unlock := func() {
		release.Do(func() {
			if locked.Load() {
				receiver.writeMu.Unlock()
			}
		})
	}
	defer unlock()
	receiver.onFile = func(n Notice) {
		if n.FileState == "complete" {
			cancel()
			unlock()
		}
	}
	name, err := a.SendFileControlled(ctx, b.LocalID, path, NewFileControl(), func(done, total int64) {
		if done > 0 && done == total {
			receiver.writeMu.Lock()
			locked.Store(true)
		}
	}, nil)
	if err != nil || name != "saved.bin" {
		t.Fatalf("saved file was not reported as complete: %q %v", name, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "saved.bin")); err != nil {
		t.Fatal(err)
	}
}

func TestCancelAfterSavedOrLostResumeRecord(t *testing.T) {
	dir := t.TempDir()
	incoming, err := filetransfer.Begin(dir, "saved.bin", 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := incoming.Append(0, []byte("data")); err != nil {
		t.Fatal(err)
	}
	digest := blake3.Sum256([]byte("data"))
	path, err := incoming.Finish(digest[:])
	if err != nil {
		t.Fatal(err)
	}
	var store resumeFiles
	defer store.close()
	received := &fileReceive{id: "token", name: "saved.bin", file: incoming, resumable: true}
	store.put("peer", received, filepath.Base(path))
	s := &Session{fileResumeSupported: true, resumeFiles: &store, resumePeer: "peer", done: make(chan struct{}), responses: make(chan response, 1)}
	payload, err := msgpack.Marshal(wire.ImageEnd{ItemID: "token"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		s.receiveFile(wire.TypeFileCancel, payload, nil)
		result := (<-s.responses).body.(wire.FileResult)
		if result.State != "complete" || result.Message != "saved.bin" {
			t.Fatalf("saved transfer result: %+v", result)
		}
	}
	store.take("peer", "token")
	s.receiveFile(wire.TypeFileCancel, payload, nil)
	if result := (<-s.responses).body.(wire.FileResult); result.State != "unknown" {
		t.Fatalf("missing transfer record reported a certain outcome: %+v", result)
	}
}

func TestCancelWithChunksInFlight(t *testing.T) {
	a, b := newPair(t)
	dir := t.TempDir()
	b.SetReceiveDir(dir)
	path := filepath.Join(t.TempDir(), "partial.bin")
	if err := os.WriteFile(path, make([]byte, 2*fileSendWindow*wire.ChunkSize), 0600); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	receiver := b.sess
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.SendFileControlled(ctx, b.LocalID, path, NewFileControl(), func(done, _ int64) {
		if done != 0 {
			return
		}
		receiver.writeMu.Lock()
		go func() {
			defer receiver.writeMu.Unlock()
			for len(receiver.responses) < fileSendWindow-1 && ctx.Err() == nil {
				time.Sleep(time.Millisecond)
			}
			cancel()
		}()
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was not confirmed: %v", err)
	}
	waitFor(t, func() bool { entries, _ := os.ReadDir(dir); return len(entries) == 0 })
	if !a.Connected() || !b.Connected() {
		t.Fatal("cancelling an in-flight transfer closed the connection")
	}
}

func TestCancelAfterDisconnectReportsUnknownResult(t *testing.T) {
	a, b := newPair(t)
	b.SetReceiveDir(t.TempDir())
	path := filepath.Join(t.TempDir(), "interrupted.bin")
	if err := os.WriteFile(path, make([]byte, 2*fileSendWindow*wire.ChunkSize), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := a.SendFileControlled(ctx, b.LocalID, path, NewFileControl(), func(done, _ int64) {
		if done > 0 {
			a.Disconnect()
			cancel()
		}
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "无法确认对方是否已保存") {
		t.Fatalf("disconnect cancellation reported a certain outcome: %v", err)
	}
}

func TestCancelWhileWaitingForReconnectReportsUnknownResult(t *testing.T) {
	a, b := newPair(t)
	b.SetReceiveDir(t.TempDir())
	path := filepath.Join(t.TempDir(), "interrupted.bin")
	if err := os.WriteFile(path, make([]byte, 2*fileSendWindow*wire.ChunkSize), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.SendFileControlled(ctx, b.LocalID, path, NewFileControl(), func(done, _ int64) {
		if done > 0 {
			a.Disconnect()
		}
	}, cancel)
	if err != ErrFileOutcomeUnknown {
		t.Fatalf("cancellation while disconnected reported a certain outcome: %v", err)
	}
}
