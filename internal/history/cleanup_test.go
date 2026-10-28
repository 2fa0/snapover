package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenRemovesInterruptedTextStagingOnly(t *testing.T) {
	dir := t.TempDir()
	texts := filepath.Join(dir, "texts")
	if err := os.Mkdir(texts, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".text-123456", ".text-789", ".text-notes", ".text-", "keep.txt"} {
		if err := os.WriteFile(filepath.Join(texts, name), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(filepath.Join(texts, ".text-123456"), old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(texts, ".text-123456")); !os.IsNotExist(err) {
		t.Fatal("interrupted text staging was retained", err)
	}
	for _, name := range []string{".text-789", ".text-notes", ".text-", "keep.txt"} {
		if _, err := os.Stat(filepath.Join(texts, name)); err != nil {
			t.Fatal("startup cleanup removed an unrelated file", name, err)
		}
	}
}
