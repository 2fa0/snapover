package history

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

func TestStreamingIndexCompatibilityAndFailure(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.entries = []Entry{{ID: "text", Text: "你好\n\"\\\t<>&", At: time.Now().UTC(), Pinned: true, SourceApp: "Editor"}}
	if err := s.saveLocked(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		t.Fatal(err)
	}
	var oldFormat struct {
		Items []Entry `json:"items"`
	}
	if err := json.Unmarshal(data, &oldFormat); err != nil {
		t.Fatal(err)
	}
	if len(oldFormat.Items) != 1 || oldFormat.Items[0] != s.entries[0] {
		t.Fatal("JSON format changed")
	}
	// A serialization error must leave the previous index intact.
	s.entries[0].At = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.saveLocked(); err == nil {
		t.Fatal("invalid date accepted")
	}
	again, err := Open(s.dir)
	if err != nil || len(again.List()) != 1 || again.List()[0] != oldFormat.Items[0] {
		t.Fatal("failed save replaced previous index")
	}
	if _, err := os.Stat(s.indexPath() + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}
}

func TestAddReusesThumbnail(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := mustPNG(t)
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, "thumbs", entry.ThumbFile))
	if err != nil || string(raw) != string(item.Thumbnail) {
		t.Fatal("thumbnail not reused")
	}
	// Items made by callers without a thumbnail still work.
	item.Thumbnail = nil
	entry, err = s.Add(DirLocal, "", item)
	if err != nil || entry.ThumbFile == "" {
		t.Fatal("thumbnail fallback missing")
	}
}

func BenchmarkSaveLargeHistory(b *testing.B) {
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	for range 1000 {
		s.entries = append(s.entries, Entry{Text: strings.Repeat("A", 64<<10), At: time.Now().UTC()})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := s.saveLocked(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAddLargeImage(b *testing.B) {
	// Generate and validate once; the measured path is history persistence.
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 4000, 10000))); err != nil {
		b.Fatal(err)
	}
	item, err := clip.FromPNG(raw.Bytes())
	if err != nil {
		b.Fatal(err)
	}
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := s.Add(DirLocal, "", item); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPrepareImagePaste(b *testing.B) {
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 2000, 2000))); err != nil {
		b.Fatal(err)
	}
	item, err := clip.FromPNG(raw.Bytes())
	if err != nil {
		b.Fatal(err)
	}
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		b.Fatal(err)
	}
	for _, name := range []string{"sync-item", "paste-item"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				var err error
				if name == "sync-item" {
					_, err = s.Item(entry.ID)
				} else {
					_, err = s.PasteItem(entry.ID)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPinPopulatedHistory(b *testing.B) {
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	for i := range 1000 {
		s.entries = append(s.entries, Entry{ID: fmt.Sprint(i), Kind: "text", Text: strings.Repeat("中", 80), At: time.Now().UTC()})
	}
	if err := s.saveLocked(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if err := s.SetPinned("999", i%2 == 0); err != nil {
			b.Fatal(err)
		}
	}
}
