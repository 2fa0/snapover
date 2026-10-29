package filetransfer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zeebo/blake3"
)

const testMaxBytes int64 = 200 << 20

func TestReceivePublishesCompleteFileWithoutOverwrite(t *testing.T) {
	dir := receiveTestDir(t)
	original := filepath.Join(dir, "报告.txt")
	if err := os.WriteFile(original, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Begin(dir, "报告.txt", 3, testMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Append(0, []byte("new")); err != nil {
		t.Fatal(err)
	}
	hash := blake3.Sum256([]byte("new"))
	path, err := f.Finish(hash[:])
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "报告 (1).txt" {
		t.Fatal(path)
	}
	got, _ := os.ReadFile(original)
	if string(got) != "original" {
		t.Fatal("overwrote existing file")
	}
	got, _ = os.ReadFile(path)
	if string(got) != "new" {
		t.Fatal(string(got))
	}
	assertNoParts(t, dir)
}

func TestRejectInvalidFilesAndChunks(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../escape", "C:\\escape", "", "..", "/absolute"} {
		if f, err := Begin(dir, name, 0, testMaxBytes); err == nil {
			f.Abort()
			t.Fatalf("accepted %q", name)
		}
	}
	for _, size := range []int64{-1, testMaxBytes + 1} {
		if f, err := Begin(dir, "file", size, testMaxBytes); err == nil {
			f.Abort()
			t.Fatalf("accepted size %d", size)
		}
	}
	f, err := Begin(dir, "file", 2, testMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Abort()
	if f.Append(1, []byte("a")) == nil || f.Append(0, []byte("abc")) == nil {
		t.Fatal("accepted invalid chunk")
	}
	if err := f.Append(0, []byte("ab")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Finish(make([]byte, 32)); err == nil {
		t.Fatal("accepted invalid hash")
	}
	assertNoParts(t, dir)
	if _, err := os.Stat(filepath.Join(dir, "file")); !os.IsNotExist(err) {
		t.Fatal("published corrupt content")
	}
}

func TestEmptyFileAndSizeBoundary(t *testing.T) {
	dir := t.TempDir()
	f, err := Begin(dir, "empty", 0, testMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	hash := blake3.Sum256(nil)
	path, err := f.Finish(hash[:])
	if err != nil {
		t.Fatal(err)
	}
	info, err := Validate(path, testMaxBytes)
	if err != nil || info.Size() != 0 {
		t.Fatalf("%v %v", info, err)
	}
	if err := os.Truncate(path, testMaxBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(path, testMaxBytes); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, testMaxBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(path, testMaxBytes); err == nil {
		t.Fatal("accepted oversized file")
	}
	if _, err := Validate(dir, testMaxBytes); err == nil {
		t.Fatal("accepted directory")
	}
}

func TestPortableFileNames(t *testing.T) {
	for input, want := range map[string]string{"CON.txt": "_CON.txt", "a:b?.txt": "a_b_.txt", "LPT1": "_LPT1", "你好.txt": "你好.txt"} {
		got, err := SafeName(input)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", input, got, err)
		}
	}
}

func TestConcurrentReceivesDoNotOverwrite(t *testing.T) {
	dir := receiveTestDir(t)
	const count = 12
	var wg sync.WaitGroup
	errors := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("content %d", i))
			f, err := Begin(dir, "same.txt", int64(len(data)), testMaxBytes)
			if err != nil {
				errors <- err
				return
			}
			defer f.Abort()
			if err := f.Append(0, data); err != nil {
				errors <- err
				return
			}
			hash := blake3.Sum256(data)
			if _, err := f.Finish(hash[:]); err != nil {
				errors <- err
			}
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != count {
		t.Fatalf("received files = %d, err = %v", len(entries), err)
	}
	contents := make(map[string]bool)
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		contents[string(data)] = true
	}
	for i := 0; i < count; i++ {
		if !contents[fmt.Sprintf("content %d", i)] {
			t.Fatalf("content %d was overwritten or lost", i)
		}
	}
	assertNoParts(t, dir)
}

func receiveTestDir(t *testing.T) string {
	t.Helper()
	base := os.Getenv("SNAPOVER_TEST_RECEIVE_DIR")
	if base == "" {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp(base, "snapover-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func assertNoParts(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if bytes.HasSuffix([]byte(e.Name()), []byte(".part")) {
			t.Fatalf("partial file left: %s", e.Name())
		}
	}
}

func TestValidatePathsKeepsExplicitSendingPerFile(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.txt")
	second := filepath.Join(dir, "b 中文.txt")
	if err := os.WriteFile(first, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	sizes, err := ValidatePaths([]string{first, second}, testMaxBytes)
	if err != nil || len(sizes) != 2 || sizes[0] != 3 || sizes[1] != 6 {
		t.Fatalf("%v %v", sizes, err)
	}
	if _, err := ValidatePaths(nil, testMaxBytes); err == nil {
		t.Fatal("accepted an empty batch")
	}
	// A batch names the entry that failed instead of dropping the whole list.
	if _, err := ValidatePaths([]string{first, dir}, testMaxBytes); err == nil || !strings.Contains(err.Error(), filepath.Base(dir)) {
		t.Fatalf("folder in batch: %v", err)
	}
	// Each file keeps the single-file limit; the batch total is not capped.
	_, err = ValidatePaths([]string{first, second}, 4)
	if err == nil || !strings.Contains(err.Error(), filepath.Base(second)) {
		t.Fatalf("over-limit entry: %v", err)
	}
	if _, err := ValidatePaths([]string{first}, 4); err != nil {
		t.Fatalf("single file within the limit: %v", err)
	}
}
