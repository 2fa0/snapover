package filetransfer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClipboardCachePreservesNativeReferencesAndCleansExpired(t *testing.T) {
	dir := t.TempDir()
	c := NewClipboardCache(dir)
	old := time.Now().Add(-time.Hour)
	for _, name := range []string{"copy-current", "copy-old"} {
		p := filepath.Join(dir, name)
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "file"), []byte("kept"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	protected := []string{filepath.Join(dir, "copy-current", "file")}
	if err := c.Clean(protected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(protected[0]); err != nil {
		t.Fatal("current clipboard deleted", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "copy-old")); !os.IsNotExist(err) {
		t.Fatal("expired copy retained")
	}
	lease, err := c.Begin(protected, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Begin(protected, 100, 100); err == nil {
		t.Fatal("concurrent receives exceeded reservation")
	}
	if err := os.Chtimes(lease.Dir, old, old); err != nil {
		t.Fatal(err)
	}
	if err := c.Clean(protected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lease.Dir); err != nil {
		t.Fatal("live transfer deleted")
	}
	lease.Release(false)
	if _, err := os.Stat(lease.Dir); !os.IsNotExist(err) {
		t.Fatal("aborted transfer retained")
	}
	// Reopening the cache still protects a clipboard written by the old process.
	if err := NewClipboardCache(dir).Clean(protected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(protected[0]); err != nil {
		t.Fatal(err)
	}
}
