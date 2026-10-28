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

func TestClipboardCacheCleansAbandonedSendsAndKeepsLiveTransfers(t *testing.T) {
	dir := t.TempDir()
	c := NewClipboardCache(dir)
	abandoned := filepath.Join(dir, "send-abandoned")
	if err := os.Mkdir(abandoned, 0700); err != nil {
		t.Fatal(err)
	}
	send, err := c.BeginSend()
	if err != nil {
		t.Fatal(err)
	}
	defer send.Release(false)
	if err := c.Clean(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Fatal("abandoned outgoing directory retained", err)
	}
	if _, err := os.Stat(send.Dir); err != nil {
		t.Fatal("maintenance removed a live send", err)
	}
	receive, err := c.Begin(nil, 100, 100)
	if err != nil {
		t.Fatal("outgoing archive blocked an independent receive", err)
	}
	receive.Release(false)
}

func TestClipboardCacheRetentionDoesNotGrowWithFileLimit(t *testing.T) {
	c := NewClipboardCache(t.TempDir())
	// A single large receive still gets space for its archive and extracted body.
	large, err := c.Begin(nil, 2<<30, 2<<30)
	if err != nil {
		t.Fatal(err)
	}
	large.Release(false)
	old := filepath.Join(c.dir, "copy-retained")
	if err := os.Mkdir(old, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(old, "file")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(clipboardCacheBytes + 1)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if lease, err := c.Begin([]string{path}, 10<<30, 100); err == nil {
		lease.Release(false)
		t.Fatal("raising file limit expanded retained-cache budget")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("admission deleted the current clipboard", err)
	}
}
