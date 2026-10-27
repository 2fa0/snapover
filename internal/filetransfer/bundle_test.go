package filetransfer

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBundleRoundTrip(t *testing.T) {
	source := t.TempDir()
	folder := filepath.Join(source, "资料 #")
	if err := os.MkdirAll(filepath.Join(folder, "空文件夹"), 0700); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"资料 #/你好.txt": "内容\n", "empty": "", "contents.zip": "ordinary file"}
	for name, body := range want {
		if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(name)), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	paths := []string{folder, filepath.Join(source, "empty"), filepath.Join(source, "contents.zip")}
	archive, err := PackBundle(context.Background(), paths, 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(archive)
	dest := t.TempDir()
	copied, err := UnpackBundle(context.Background(), archive, dest, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(copied) != 3 || filepath.Base(copied[0]) != "资料 #" {
		t.Fatalf("wrong top-level items: %v", copied)
	}
	for name, body := range want {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name)))
		if err != nil || string(got) != body {
			t.Fatalf("%s changed: %s %v", name, got, err)
		}
	}
	if st, err := os.Stat(filepath.Join(dest, "资料 #", "空文件夹")); err != nil || !st.IsDir() {
		t.Fatal("empty directory lost", err)
	}
}

func TestPackBundleLimitsLinksAndCancellation(t *testing.T) {
	source := t.TempDir()
	file := filepath.Join(source, "file")
	if err := os.WriteFile(file, []byte("four"), 0600); err != nil {
		t.Fatal(err)
	}
	if p, err := PackBundle(context.Background(), []string{file}, 3, nil); err == nil {
		os.Remove(p)
		t.Fatal("oversized file accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := PackBundle(ctx, []string{file}, 100, nil); !errors.Is(err, context.Canceled) {
		os.Remove(p)
		t.Fatalf("canceled work continued: %v", err)
	}
	stop := errors.New("clipboard changed")
	if p, err := PackBundle(context.Background(), []string{file}, 100, func() error { return stop }); !errors.Is(err, stop) {
		os.Remove(p)
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(source, "link")
		if err := os.Symlink(file, link); err != nil {
			t.Fatal(err)
		}
		if p, err := PackBundle(context.Background(), []string{source}, 100, nil); err == nil {
			os.Remove(p)
			t.Fatal("followed symlink")
		}
	}
}

func makeBundle(t *testing.T, names []string, method uint16) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "test.zip")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, name := range names {
		out, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write([]byte("data")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestUnpackBundleRejectsUnsafePathsAndExpansion(t *testing.T) {
	cases := [][]string{{"../escape"}, {"/absolute"}, {"C:/drive"}, {"a\\b"}, {"a/../b"}, {"CON"}, {"a.", "a"}, {"a", "A"}, {"a/x", "A/y"}, {strings.Repeat("x", 241)}}
	for _, names := range cases {
		t.Run(strings.Join(names, "|"), func(t *testing.T) {
			if _, err := UnpackBundle(context.Background(), makeBundle(t, names, zip.Store), t.TempDir(), 100); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	if _, err := UnpackBundle(context.Background(), makeBundle(t, []string{"file"}, zip.Deflate), t.TempDir(), 100); err == nil {
		t.Fatal("compressed archive accepted")
	}
	if _, err := UnpackBundle(context.Background(), makeBundle(t, []string{"file"}, zip.Store), t.TempDir(), 3); err == nil {
		t.Fatal("expanded size over limit")
	}
	archive := makeBundle(t, []string{"file"}, zip.Store)
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	index := strings.Index(string(raw), "data")
	if index < 0 {
		t.Fatal("missing payload")
	}
	raw[index] = 'X'
	if err := os.WriteFile(archive, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := UnpackBundle(context.Background(), archive, t.TempDir(), 100); err == nil {
		t.Fatal("bad checksum accepted")
	}
}
