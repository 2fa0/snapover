package filetransfer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/zeebo/blake3"
)

func TestCleanStaleTransfersKeepsLiveRecentAndUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	live, err := Begin(dir, "saved.txt", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Abort()
	old := time.Now().Add(-staleTransferAge - time.Hour)
	if err := os.Chtimes(live.stageDir, old, old); err != nil {
		t.Fatal(err)
	}
	protected := []string{live.file.Name()}
	for _, name := range []string{".snapover-123.part", ".snapover-check-456.part.saved", ".snapover-clipboard-123.zip", ".snapover-incoming-999"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("saved file"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		protected = append(protected, path)
	}
	for _, name := range []string{".snapover-incoming-123", ".snapover-incoming-456", ".snapover-incoming-789", ".snapover-incoming-notes"} {
		path := filepath.Join(dir, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		file := "contents.part"
		if name == ".snapover-incoming-789" {
			file = "user-file"
		}
		if err := os.WriteFile(filepath.Join(path, file), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
		if name != ".snapover-incoming-456" {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
		if name != ".snapover-incoming-123" {
			protected = append(protected, filepath.Join(path, file))
		}
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(dir, ".snapover-incoming-777")
		if err := os.Symlink(live.stageDir, link); err != nil {
			t.Fatal(err)
		}
		protected = append(protected, link)
		// The live receive is also protected when accessed through a folder alias.
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(dir, alias); err != nil {
			t.Fatal(err)
		}
		if err := CleanStaleTransfers(alias); err != nil {
			t.Fatal(err)
		}
	}
	if err := CleanStaleTransfers(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".snapover-incoming-123")); !os.IsNotExist(err) {
		t.Fatal("stale staging directory retained", err)
	}
	for _, path := range protected {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("cleanup removed protected data: %s %v", path, err)
		}
	}
}

func TestReceivePreservesSavedFileWithTemporaryLookingName(t *testing.T) {
	dir := t.TempDir()
	name := ".snapover-incoming-123"
	f, err := Begin(dir, name, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Append(0, []byte("x")); err != nil {
		t.Fatal(err)
	}
	hash := blake3.Sum256([]byte("x"))
	path, err := f.Finish(hash[:])
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-staleTransferAge - time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := CleanStaleTransfers(dir); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "x" {
		t.Fatal("saved file was treated as staging", err)
	}
	if _, err := os.Stat(f.stageDir); !os.IsNotExist(err) {
		t.Fatal("successful receive left its staging directory", err)
	}
}
