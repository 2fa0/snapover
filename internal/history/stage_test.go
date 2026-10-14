package history

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

func TestStageImageCopiesWithoutReplacingBlob(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromPNG(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(dir, "blobs", entry.ImageFile)
	before, err := os.ReadFile(blob)
	if err != nil {
		t.Fatal(err)
	}
	path, err := s.StageImage(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if path == blob || filepath.Dir(path) != filepath.Join(dir, "opened") {
		t.Fatalf("staged path = %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, before) {
		t.Fatal("staged file is not the stored PNG")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("staged mode = %o", info.Mode().Perm())
	}
	after, err := os.ReadFile(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("staging rewrote the history blob")
	}
	if _, err := s.StageImage("missing"); !os.IsNotExist(err) {
		t.Fatalf("missing image: %v", err)
	}
	text, err := clip.FromText("hello")
	if err != nil {
		t.Fatal(err)
	}
	textEntry, err := s.Add(DirLocal, "", text)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StageImage(textEntry.ID); !os.IsNotExist(err) {
		t.Fatalf("text image: %v", err)
	}
}

func TestOpenAndCleanRemoveStagedImages(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	opened := filepath.Join(dir, "opened")
	if err := os.MkdirAll(opened, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(opened, "stale.png")
	if err := os.WriteFile(stale, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.CleanOpened()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("clean left staged file: %v", err)
	}
	if err := os.WriteFile(stale, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("next launch left staged file: %v", err)
	}
}

func TestStageImageReapsOnlyExpiredCopies(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromPNG(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "opened", "old.png")
	recent := filepath.Join(dir, "opened", "recent.png")
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recent, []byte("recent"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-stagedImageTTL - time.Second)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	path, err := s.StageImage(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expired copy remained: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent copy was removed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("new copy was removed: %v", err)
	}
}

func TestCleanOpenedStopsConcurrentAndLateStages(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromPNG(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StageImage(entry.ID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			<-start
			if _, err := s.StageImage(entry.ID); err != nil && !errors.Is(err, os.ErrClosed) {
				t.Errorf("concurrent stage: %v", err)
			}
		})
	}
	workers.Go(func() {
		<-start
		s.CleanOpened()
	})
	close(start)
	workers.Wait()
	if _, err := s.StageImage(entry.ID); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("late stage after cleanup: %v", err)
	}
	files, err := os.ReadDir(filepath.Join(dir, "opened"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("cleanup left %d staged copies", len(files))
	}
	if _, err := s.Image(entry.ID); err != nil {
		t.Fatalf("stopping staging affected the history blob: %v", err)
	}
}
