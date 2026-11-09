package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/filetransfer"
	"github.com/oklog/ulid/v2"
)

func TestPinnedFileKeepsOwnerExecutePermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix execute permissions")
	}
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := writeHistoryFile(t, t.TempDir(), "run.sh", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(p, 0755); err != nil {
		t.Fatal(err)
	}
	e, err := s.Add(DirLocal, "", fileItem(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(e.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	item, err := s.PasteItem(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(item.Files[0])
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("snapshot should remain private and executable: %v, %v", info, err)
	}
}

func TestPinnedFilesFollowMovedOrCopiedStore(t *testing.T) {
	for _, format := range []string{"journal", "snapshot", "legacy"} {
		for _, copyStore := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/copy=%t", format, copyStore), func(t *testing.T) {
				base := t.TempDir()
				s, err := Open(filepath.Join(base, "old"))
				if err != nil {
					t.Fatal(err)
				}
				source := writeHistoryFile(t, t.TempDir(), "document.txt", "saved snapshot")
				e, err := s.Add(DirLocal, "", fileItem(t, source))
				if err != nil {
					t.Fatal(err)
				}
				if err := s.SetPinned(e.ID, true); err != nil {
					t.Fatal(err)
				}
				oldCopy := s.List()[0].PinFiles[0]
				if format == "legacy" {
					s.entries[0].FilePaths = s.entries[0].PinFiles
					s.entries[0].PinFiles = nil
				}
				if format != "journal" {
					if err := s.saveLocked(); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				moved := filepath.Join(base, "moved")
				if copyStore {
					err = os.CopyFS(moved, os.DirFS(s.dir))
					if err == nil {
						err = os.WriteFile(oldCopy, []byte("stale original store"), 0600)
					}
				} else {
					err = os.Rename(s.dir, moved)
				}
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					next, err := Open(moved)
					if err != nil {
						t.Fatal(err)
					}
					item, err := next.PasteItem(e.ID)
					if err != nil {
						t.Fatal(err)
					}
					want := filepath.Join(next.pinFilesDir(e.ID), "document.txt")
					if len(item.Files) != 1 || item.Files[0] != want {
						t.Fatalf("snapshot still refers to the old store: %v", item.Files)
					}
					body, err := os.ReadFile(item.Files[0])
					if err != nil || string(body) != "saved snapshot" {
						t.Fatalf("read the wrong store: %q, %v", body, err)
					}
				}
			})
		}
	}
}

func fileItem(t *testing.T, paths ...string) clip.Item {
	t.Helper()
	item, err := clip.FromFiles(paths)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func writeHistoryFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFileHistoryKeepsNamesAfterSourceRemovalAndRestart(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	folder := filepath.Join(source, "项目资料")
	writeHistoryFile(t, folder, "报告.txt", "original")
	if err := os.MkdirAll(filepath.Join(folder, "空目录"), 0700); err != nil {
		t.Fatal(err)
	}
	item := fileItem(t, folder)
	item.SourceApp = "Finder"
	entry, err := s.Add(DirLocal, "Mac", item)
	if err != nil {
		t.Fatal(err)
	}
	if entry.FileName != "项目资料" || !entry.Folder || entry.FileCount != 1 || entry.Size != 0 || Title(entry) != "项目资料" || entry.FileBundle != "" {
		t.Fatalf("incorrect metadata: %+v", entry)
	}
	files, err := os.ReadDir(filepath.Join(s.dir, "files"))
	if err != nil || len(files) != 0 {
		t.Fatalf("history stored file bodies: %v %v", files, err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	s, err = Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 1 || list[0].SourceApp != "Finder" || list[0].FileName != "项目资料" || list[0].FileBundle != "" {
		t.Fatal(list)
	}
	preview, err := s.FilePreview(entry.ID)
	if err != nil || len(preview.Items) != 2 || !preview.Directory {
		t.Fatalf("preview lost: %+v %v", preview, err)
	}
	if _, err := s.PasteItem(entry.ID); err == nil {
		t.Fatal("file history still prepared a paste body")
	}
}

func TestFileHistoryPreviewOneLevelAndTwentyItemLimit(t *testing.T) {
	for _, count := range []int{0, 20, 25} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			folder := filepath.Join(t.TempDir(), "文件夹")
			if err := os.Mkdir(folder, 0700); err != nil {
				t.Fatal(err)
			}
			if count > 0 {
				writeHistoryFile(t, folder, "00-child/deeper.txt", "nested")
				for i := 1; i < count; i++ {
					writeHistoryFile(t, folder, fmt.Sprintf("file-%02d.txt", i), "file")
				}
			}
			entry, err := s.Add(DirLocal, "", fileItem(t, folder))
			if err != nil {
				t.Fatal(err)
			}
			p, err := s.FilePreview(entry.ID)
			if err != nil {
				t.Fatal(err)
			}
			if p.Name != "文件夹" || len(p.Items) != min(count, 20) || p.More != (count > 20) {
				t.Fatalf("bad bounded preview: %+v", p)
			}
			for _, item := range p.Items {
				if item.Name == "deeper.txt" || item.Depth != 0 || strings.ContainsAny(item.Name, "/\\") {
					t.Fatal("preview recursed", item)
				}
			}
			if count > 0 && count <= MaxFilePreviewItems && (!p.Items[0].Directory || p.Items[0].Name != "00-child") {
				t.Fatal("child folder should be visible without expansion")
			}
		})
	}
}

func TestFileHistoryNamesMultiSelectionAndSearch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	first := writeHistoryFile(t, dir, "报告 final.pdf", "pdf")
	second := writeHistoryFile(t, dir, "another-file.txt", "other")
	entry, err := s.Add(DirRemote, "Windows", fileItem(t, first))
	if err != nil {
		t.Fatal(err)
	}
	if Title(entry) != "报告 final.pdf" || entry.Folder || entry.Size != 3 {
		t.Fatal("filename not retained", entry)
	}
	multi, err := s.Add(DirLocal, "", fileItem(t, first, second))
	if err != nil {
		t.Fatal(err)
	}
	if multi.FileCount != 2 || Title(multi) != "报告 final.pdf 等 2 项" {
		t.Fatal("multi title", multi)
	}
	p, err := s.FilePreview(multi.ID)
	if err != nil || len(p.Items) != 2 || p.Items[1].Name != "another-file.txt" {
		t.Fatalf("multi preview: %+v %v", p, err)
	}
	found, err := s.Search(context.Background(), "ANOTHER-file")
	if err != nil || len(found) != 1 || found[0].ID != multi.ID {
		t.Fatalf("secondary filename not searchable: %+v %v", found, err)
	}
}

func TestFileHistoryDoesNotCountUnpinnedBodiesTowardCapacity(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := writeHistoryFile(t, t.TempDir(), "file.txt", strings.Repeat("payload", 1000))
	if _, err := s.Add(DirLocal, "", fileItem(t, p)); err != nil {
		t.Fatal(err)
	}
	usage, err := s.Usage()
	if err != nil || usage.ContentBytes >= int64(len(strings.Repeat("payload", 1000))) {
		t.Fatalf("unpinned file bodies counted as history content: %+v", usage)
	}
}

func TestPinnedFilesStayPasteableAfterSourceRemoved(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p := writeHistoryFile(t, dir, "file.txt", "keep-me")
	entry, err := s.Add(DirLocal, "", fileItem(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(entry.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	list := s.List()
	var pinned Entry
	for _, e := range list {
		if e.ID == entry.ID {
			pinned = e
		}
	}
	if !HasFileSnapshot(pinned) || !FilesAvailable(pinned) {
		t.Fatalf("pin did not snapshot: %+v", pinned)
	}
	item, err := s.PasteItem(entry.ID)
	if err != nil || len(item.Files) != 1 {
		t.Fatalf("paste after delete: %v %+v", err, item)
	}
	body, err := os.ReadFile(item.Files[0])
	if err != nil || string(body) != "keep-me" {
		t.Fatalf("snapshot body: %s %v", body, err)
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	item, err = reopened.PasteItem(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(item.Files[0])
	if err != nil || string(body) != "keep-me" {
		t.Fatalf("reopened snapshot: %s %v", body, err)
	}
	usage, err := reopened.Usage()
	if err != nil || usage.PinnedBytes < int64(len("keep-me")) {
		t.Fatalf("pinned snapshot not counted: %+v", usage)
	}
}

func TestFileHistoryDropsLegacyBundlesOnOpen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := writeHistoryFile(t, t.TempDir(), "报告.txt", "original")
	archive, err := filetransfer.PackBundle(context.Background(), []string{p}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := ulid.Make().String()
	bundle := id + ".zip"
	if err := os.Rename(archive, filepath.Join(s.dir, "files", bundle)); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	_, err = s.addLocked(Entry{ID: id, At: time.Now(), Kind: string(clip.KindFiles), FileName: "报告.txt", FileCount: 1, Size: 8, FileBundle: bundle})
	s.publishLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 1 || list[0].FileBundle != "" || list[0].FileName != "报告.txt" {
		t.Fatalf("legacy bundle not dropped: %+v", list)
	}
	preview, err := s.FilePreview(list[0].ID)
	if err != nil || preview.Name != "报告.txt" {
		t.Fatalf("legacy preview lost: %+v %v", preview, err)
	}
	files, err := os.ReadDir(filepath.Join(s.dir, "files"))
	if err != nil || len(files) != 0 {
		t.Fatalf("legacy archive retained: %v %v", files, err)
	}
}

func TestFileHistoryFailuresLeaveNoPartialAssets(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := writeHistoryFile(t, t.TempDir(), "file.txt", "payload")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.AddFiles(ctx, DirLocal, "", fileItem(t, p)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
	if err := os.Mkdir(s.journalPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(DirLocal, "", fileItem(t, p)); err == nil {
		t.Fatal("journal failure ignored")
	}
	files, err := os.ReadDir(filepath.Join(s.dir, "files"))
	if err != nil || len(files) != 0 || len(s.List()) != 0 {
		t.Fatalf("failed save left assets: %v %v", files, err)
	}
	if _, err := s.bundlePath("../escape.zip"); err == nil {
		t.Fatal("unsafe path accepted")
	}
}

func TestFileHistoryChronologyMatchesJournalReplay(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := Entry{ID: ulid.Make().String(), At: time.Now().Add(-time.Minute), Kind: "text", Text: "old"}
	newer := Entry{ID: ulid.Make().String(), At: time.Now(), Kind: "text", Text: "new"}
	s.mu.Lock()
	_, err = s.addLocked(newer)
	if err == nil {
		_, err = s.addLocked(old)
	}
	s.publishLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if list := s.List(); len(list) != 2 || list[0].ID != newer.ID {
		t.Fatal("replay changed completion order", list)
	}
}

type pausedFileContext struct {
	context.Context
	once             sync.Once
	started, release chan struct{}
}

func (c *pausedFileContext) Err() error {
	c.once.Do(func() { close(c.started); <-c.release })
	return c.Context.Err()
}

func TestClearRevokesFileSnapshotStillBeingRead(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := writeHistoryFile(t, t.TempDir(), "file.txt", "snapshot")
	ctx := &pausedFileContext{Context: context.Background(), started: make(chan struct{}), release: make(chan struct{})}
	result := make(chan error, 1)
	go func() { _, err := s.AddFiles(ctx, DirLocal, "", fileItem(t, p)); result <- err }()
	<-ctx.started
	cleared := make(chan error, 1)
	go func() { cleared <- s.Clear() }()
	select {
	case err := <-cleared:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(ctx.release)
		t.Fatal("clear waited for file body I/O")
	}
	close(ctx.release)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal("late file snapshot repopulated cleared history", err)
	}
	files, err := os.ReadDir(filepath.Join(s.dir, "files"))
	if err != nil || len(files) != 0 || len(s.List()) != 0 {
		t.Fatal("revoked snapshot left files", files, err)
	}
}

func TestFileHistoryDoesNotScanDescendantsOrLoseSelectedRoots(t *testing.T) {
	s, _ := Open(t.TempDir())
	folder := t.TempDir()
	writeHistoryFile(t, folder, "child/deep/hidden.txt", "not scanned")
	// Removing access to descendants must not prevent recording the parent.
	if err := os.Chmod(filepath.Join(folder, "child"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(folder, "child"), 0700)
	item := fileItem(t, folder)
	entry, err := s.Add(DirLocal, "", item)
	if err != nil || entry.Size != 0 || len(entry.FileTree) != 1 {
		t.Fatal(entry, err)
	}
	second := writeHistoryFile(t, t.TempDir(), "second.txt", "hello")
	entry, err = s.Add(DirLocal, "", fileItem(t, folder, second))
	if err != nil || entry.FileCount != 2 || len(entry.FilePaths) != 2 || len(entry.FileTree) != 2 || entry.Size != 5 {
		t.Fatal(entry, err)
	}
}

func TestPinnedFilesPreserveRevealPathRemoveSnapshotOnUnpinAndEnforceBudget(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p := writeHistoryFile(t, dir, "spec.txt", "pinned-payload")
	entry, err := s.Add(DirLocal, "", fileItem(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(entry.ID, true); err != nil {
		t.Fatal(err)
	}
	pinned := s.List()[0]
	if got, ok := RevealFilePath(pinned); !ok || got != p {
		t.Fatalf("reveal path should prefer live original %q, got %q (%v)", p, got, ok)
	}
	if len(pinned.FilePaths) != 1 || pinned.FilePaths[0] != p || len(pinned.PinFiles) != 1 {
		t.Fatalf("original FilePaths should stay intact: %+v", pinned)
	}

	// Unpinning must remove the copied snapshot directory and clear PinFiles.
	if err := s.SetPinned(entry.ID, false); err != nil {
		t.Fatal(err)
	}
	unpinned := s.List()[0]
	if HasFileSnapshot(unpinned) || len(unpinned.PinFiles) != 0 {
		t.Fatalf("unpin left snapshot references: %+v", unpinned)
	}
	if _, err := os.Stat(s.pinFilesDir(entry.ID)); !os.IsNotExist(err) {
		t.Fatalf("unpin did not remove snapshot dir: %v", err)
	}

	// Pinning a file larger than remaining capacity must fail cleanly without leaving partial files.
	if err := s.SetCapacity(4); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(entry.ID, true); err == nil {
		t.Fatal("pinning file exceeding capacity succeeded")
	}
	if _, err := os.Stat(s.pinFilesDir(entry.ID)); !os.IsNotExist(err) {
		t.Fatalf("failed pin left snapshot dir: %v", err)
	}
}
