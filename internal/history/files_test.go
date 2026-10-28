package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/filetransfer"
	"github.com/oklog/ulid/v2"
)

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
	if entry.FileName != "项目资料" || !entry.Folder || entry.FileCount != 1 || entry.Size != 8 || Title(entry) != "项目资料" || entry.FileBundle != "" {
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
			if count > 0 && (!p.Items[0].Directory || p.Items[0].Name != "00-child") {
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

func TestFileHistoryDoesNotCountBodiesTowardCapacity(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := writeHistoryFile(t, t.TempDir(), "file.txt", strings.Repeat("payload", 1000))
	pinned, err := s.Add(DirLocal, "", fileItem(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(pinned.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(DirLocal, "", fileItem(t, p)); err != nil {
		t.Fatal(err)
	}
	usage, err := s.Usage()
	if err != nil || usage.ContentBytes >= int64(pinned.Size) || usage.PinnedBytes >= int64(pinned.Size) {
		t.Fatalf("file bodies counted as history content: %+v size=%d", usage, pinned.Size)
	}
	if err := s.SetCapacity(1); err != nil {
		t.Fatal(err)
	}
	if list := s.List(); len(list) != 2 {
		t.Fatal("metadata-only file history was pruned", list)
	}
	orphan := filepath.Join(s.dir, "files", ulid.Make().String()+".zip")
	if err := os.WriteFile(orphan, []byte("orphan"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan not removed", err)
	}
	if len(s.List()) != 1 {
		t.Fatal("clear removed pin")
	}
	files, err := os.ReadDir(filepath.Join(s.dir, "files"))
	if err != nil || len(files) != 0 {
		t.Fatalf("file history left bodies: %v %v", files, err)
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
	if _, err := s.AddFiles(ctx, DirLocal, "", fileItem(t, p), 100); !errors.Is(err, context.Canceled) {
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
	go func() { _, err := s.AddFiles(ctx, DirLocal, "", fileItem(t, p), 100); result <- err }()
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
