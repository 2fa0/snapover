package filetransfer

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, fs string
		size     int64
		free     uint64
		fail     bool
	}{
		{"fat32 boundary", "FAT32", 1<<32 - 1, 1 << 40, false},
		{"fat32 oversized", "FAT32", 1 << 32, 1 << 40, true},
		{"mac fat", "msdos", 1 << 32, 1 << 40, true},
		{"large exfat", "exFAT", 8 << 30, 1 << 40, false},
		{"full", "NTFS", 1024, 1023, true},
		{"exact fit", "APFS", 1024, 1024, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSpace(tc.size, tc.free, tc.fs); (err != nil) != tc.fail {
				t.Fatal(err)
			}
		})
	}
}

func TestLargeSparseFileValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(5 << 30); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	info, err := Validate(path, math.MaxInt64)
	if err != nil || info.Size() != 5<<30 {
		t.Fatalf("large file rejected: %v", err)
	}
}

func TestWriteFailureDoesNotPublishPartial(t *testing.T) {
	dir := t.TempDir()
	f, err := Begin(dir, "result.bin", 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	f.file.Close()
	if err = f.Append(0, []byte("data")); err == nil {
		t.Fatal("closed disk handle accepted write")
	}
	if f.Written != 0 {
		t.Fatal("failed write advanced progress")
	}
	f.Abort()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial remains: %v %v", entries, err)
	}
}
