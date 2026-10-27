package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/filetransfer"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/wire"
	"github.com/zeebo/blake3"
)

func clipboardFiles(t *testing.T) clip.Item {
	t.Helper()
	dir := t.TempDir()
	folder := filepath.Join(dir, "文件夹")
	if err := os.MkdirAll(filepath.Join(folder, "空目录"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "内容.txt"), []byte("hello\n中文"), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "contents.zip")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromFiles([]string{folder, file})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func observeFiles(t *testing.T, e *Engine, item clip.Item) {
	t.Helper()
	if err := e.Board.Write(item); err != nil {
		t.Fatal(err)
	}
	seq, err := e.Board.Sequence()
	if err != nil {
		t.Fatal(err)
	}
	e.HandleLocal(seq, item)
}

func TestAutomaticFileClipboardRoundTripAndEcho(t *testing.T) {
	a, b := newPair(t)
	item := clipboardFiles(t)
	results := make(chan Notice, 16)
	a.mu.Lock()
	a.OnNotice = func(n Notice) error {
		if n.Kind == NoticeSync {
			results <- n
		}
		return nil
	}
	a.mu.Unlock()
	observeFiles(t, a, item)
	waitFor(t, func() bool { got, _ := b.Board.Snapshot(); return got.Kind == clip.KindFiles })
	got, _ := b.Board.Snapshot()
	if len(got.Files) != 2 || filepath.Base(got.Files[0]) != "文件夹" {
		t.Fatalf("paths lost: %v", got.Files)
	}
	raw, err := os.ReadFile(filepath.Join(got.Files[0], "内容.txt"))
	if err != nil || string(raw) != "hello\n中文" {
		t.Fatal("file changed", err)
	}
	if st, err := os.Stat(filepath.Join(got.Files[0], "空目录")); err != nil || !st.IsDir() {
		t.Fatal("empty folder lost", err)
	}
	if st, err := os.Stat(got.Files[1]); err != nil || st.Size() != 0 {
		t.Fatal("empty file lost", err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case n := <-results:
			if n.ClipboardState == "complete" {
				goto confirmed
			}
			if n.ClipboardState == "error" {
				t.Fatal(n.Message)
			}
		case <-deadline:
			t.Fatal("missing native publication confirmation")
		}
	}
confirmed:
	seq, _ := b.Board.Sequence()
	b.HandleLocal(seq, got)
	b.mu.Lock()
	haveLocal := b.haveLast
	b.mu.Unlock()
	if haveLocal || len(b.sess.out) != 0 {
		t.Fatal("received files looped back")
	}
}

// Hold a real transfer between ready and end to exercise native generations
// and remote arrival order independently of the two-second local grace period.
func stagedClipboard(t *testing.T, a *Engine, item clip.Item) (*Session, []byte, chan wire.FileResult) {
	t.Helper()
	archive, err := filetransfer.PackBundle(context.Background(), item.Files, 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(archive)
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	s := a.sess
	a.mu.Unlock()
	results := make(chan wire.FileResult, 4)
	s.fileResultMu.Lock()
	s.fileSendID = "staged"
	s.fileResults = results
	s.fileResultMu.Unlock()
	if err := s.write(wire.TypeFileBegin, wire.FileBegin{ItemID: "staged", Clipboard: true, Name: "ignored.zip", TotalSize: int64(len(raw))}); err != nil {
		t.Fatal(err)
	}
	result := fileResult(t, results)
	if result.State != "ready" {
		t.Fatalf("not ready: %+v", result)
	}
	return s, raw, results
}

func fileResult(t *testing.T, results <-chan wire.FileResult) wire.FileResult {
	t.Helper()
	select {
	case r := <-results:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("no transfer result")
	}
	return wire.FileResult{}
}

func endStagedClipboard(t *testing.T, s *Session, raw []byte) {
	t.Helper()
	for offset := 0; offset < len(raw); {
		end := min(offset+wire.ChunkSize, len(raw))
		if err := s.write(wire.TypeFileChunk, wire.FileChunk{ItemID: "staged", Offset: int64(offset), Data: raw[offset:end]}); err != nil {
			t.Fatal(err)
		}
		offset = end
	}
	hash := blake3.Sum256(raw)
	if err := s.write(wire.TypeFileEnd, wire.FileEnd{ItemID: "staged", Hash: hash[:]}); err != nil {
		t.Fatal(err)
	}
}

func TestFileClipboardCannotOverwriteNewerCopies(t *testing.T) {
	for _, newer := range []string{"local", "remote", "paused", "text-only"} {
		t.Run(newer, func(t *testing.T) {
			a, b := newPair(t)
			s, raw, results := stagedClipboard(t, a, clipboardFiles(t))
			text := mustText(t, "newer copy")
			switch newer {
			case "local":
				if err := b.Board.Write(text); err != nil {
					t.Fatal(err)
				}
			case "remote":
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := a.SendClipboard(ctx, b.LocalID, text); err != nil {
					t.Fatal(err)
				}
			case "paused":
				b.SetPolicy(true, false, 0)
			case "text-only":
				b.SetPolicy(false, true, 0)
			}
			endStagedClipboard(t, s, raw)
			result := fileResult(t, results)
			if result.State != "error" {
				t.Fatalf("stale file published: %+v", result)
			}
			got, _ := b.Board.Snapshot()
			if got.Kind == clip.KindFiles || (newer == "local" || newer == "remote") && got.Text != text.Text {
				t.Fatal("new copy overwritten")
			}
		})
	}
}

func TestFileClipboardCancellationKeepsTextMoving(t *testing.T) {
	a, b := newPair(t)
	a.mu.Lock()
	sender := a.sess
	a.mu.Unlock()
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	original := sender.fileClipboardCheck
	sender.fileClipboardCheck = func(item queuedItem) error { once.Do(func() { close(started); <-release }); return original(item) }
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	observeFiles(t, a, clipboardFiles(t))
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("file worker not started")
	}
	text := mustText(t, "copy while files are preparing")
	if err := a.Board.Write(text); err != nil {
		t.Fatal(err)
	}
	seq, _ := a.Board.Sequence()
	a.HandleLocal(seq, text)
	waitWrite(t, b.Board.(*memBoard), text.Hash)
	close(release)
	waitFor(t, func() bool { return len(sender.fileClipboardJobs) == 0 })
	got, _ := b.Board.Snapshot()
	if got.Kind != clip.KindText {
		t.Fatal("old file replaced new text")
	}
}

func TestFileClipboardPolicyAndOldPeer(t *testing.T) {
	for _, mode := range []string{"paused", "manual", "text-only", "old-peer"} {
		t.Run(mode, func(t *testing.T) {
			a, b := newPair(t)
			switch mode {
			case "paused":
				a.SetPolicy(true, false, 0)
			case "manual":
				a.SetManualSync(true)
			case "text-only":
				a.SetPolicy(false, true, 0)
			case "old-peer":
				a.sess.fileClipboardSupported = false
			}
			observeFiles(t, a, clipboardFiles(t))
			if len(a.sess.out) != 0 || len(a.sess.fileClipboardJobs) != 0 {
				t.Fatal("policy admitted files")
			}
			got, _ := b.Board.Snapshot()
			if got.Kind != "" {
				t.Fatal("peer clipboard changed")
			}
		})
	}
}

func TestUnfinishedFileClipboardIsCleanedOnDisconnect(t *testing.T) {
	a, b := newPair(t)
	dir := t.TempDir()
	b.sess.fileCache = filetransfer.NewClipboardCache(dir)
	s, _, _ := stagedClipboard(t, a, clipboardFiles(t))
	s.Close()
	waitFor(t, func() bool { entries, _ := os.ReadDir(dir); return len(entries) == 0 })
	got, _ := b.Board.Snapshot()
	if got.Kind == clip.KindFiles {
		t.Fatal("incomplete clipboard published")
	}
}

func TestFileClipboardRejectsReceiverLimit(t *testing.T) {
	a, b := newPair(t)
	b.SetFileLimit(1)
	results := make(chan Notice, 16)
	a.mu.Lock()
	a.OnNotice = func(n Notice) error {
		if n.Kind == NoticeSync {
			results <- n
		}
		return nil
	}
	a.mu.Unlock()
	observeFiles(t, a, clipboardFiles(t))
	deadline := time.After(3 * time.Second)
	for {
		select {
		case n := <-results:
			if n.ClipboardState == "error" {
				if !strings.Contains(n.Message, "上限") {
					t.Fatal(n.Message)
				}
				return
			}
		case <-deadline:
			t.Fatal("receiver size limit not enforced")
		}
	}
}

func TestReceivedFileClipboardConflictIsKeptInHistory(t *testing.T) {
	a, b := newPair(t)
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.OnNotice = func(n Notice) error {
		if n.Kind == NoticeRemote {
			_, err := store.Add(history.DirRemote, n.PeerName, n.Item)
			return err
		}
		return nil
	}
	b.mu.Unlock()
	sender, raw, results := stagedClipboard(t, a, clipboardFiles(t))
	newer := mustText(t, "keep the new local copy")
	if err := b.Board.Write(newer); err != nil {
		t.Fatal(err)
	}
	endStagedClipboard(t, sender, raw)
	result := fileResult(t, results)
	if result.State != "error" || !strings.Contains(result.Message, "历史") {
		t.Fatalf("wrong publication result: %+v", result)
	}
	got, _ := b.Board.Snapshot()
	if got.Text != newer.Text {
		t.Fatal("new local copy was overwritten")
	}
	entries := store.List()
	if len(entries) != 1 || entries[0].Kind != "files" || entries[0].Dir != history.DirRemote {
		t.Fatal("received files missing from history", entries)
	}
	if _, err := store.FilePreview(entries[0].ID); err != nil {
		t.Fatal("saved file preview unavailable", err)
	}
}
