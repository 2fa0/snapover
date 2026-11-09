package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileClipboardTitle(t *testing.T) {
	if got := fileClipboardTitle(nil); got != "文件" {
		t.Fatalf("empty: %q", got)
	}
	if got := fileClipboardTitle([]string{"/tmp/report.pdf"}); got != "report.pdf" {
		t.Fatalf("one file: %q", got)
	}
	if got := fileClipboardTitle([]string{"/tmp/a.txt", "/tmp/b.txt"}); got != "a.txt 等 2 项" {
		t.Fatalf("batch: %q", got)
	}
}

func TestEstimateFilesSizeStopsAtHint(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, make([]byte, 3<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(dir, "small.bin")
	if err := os.WriteFile(small, make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := estimateFilesSize([]string{small}, largeProgressHint); got != 100 {
		t.Fatalf("small total %d", got)
	}
	got := estimateFilesSize([]string{big, small}, 2<<20)
	if got < 2<<20 {
		t.Fatalf("did not reach stop hint: %d", got)
	}
	if got > 3<<20 {
		t.Fatalf("walked past the first file: %d", got)
	}
}
