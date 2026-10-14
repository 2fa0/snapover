package history

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
)

func TestOldImagePreviewIsGeneratedOnceAndDeletedWithRecord(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 1791, 438))); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromPNG(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	item.Preview = nil
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.Preview(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != 1120 || cfg.Height != 274 {
		t.Fatalf("detailed preview: %+v, %v", cfg, err)
	}
	if err := os.Remove(filepath.Join(dir, "blobs", entry.ImageFile)); err != nil {
		t.Fatal(err)
	}
	again, err := s.Preview(entry.ID)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatal("cached preview required the original")
	}
	thumb, err := s.Thumb(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	tc, _ := png.DecodeConfig(bytes.NewReader(thumb))
	if tc.Width != 160 {
		t.Fatal("list thumbnail was enlarged")
	}
	if err := s.Delete(entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "previews", entry.ID+".png")); !os.IsNotExist(err) {
		t.Fatal("deleted record retained preview cache")
	}
}

func TestOversizedOldImageFallsBackWithoutDecodingOriginal(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := mustPNG(t)
	// A valid oversized header with a tiny IDAT: the size check must reject it
	// before PNG decoding can allocate original pixels.
	raw := bytes.Clone(item.PNG)
	binary.BigEndian.PutUint32(raw[16:20], 3000)
	binary.BigEndian.PutUint32(raw[20:24], 3000)
	binary.BigEndian.PutUint32(raw[29:33], crc32.ChecksumIEEE(raw[12:29]))
	item.PNG, item.Preview = raw, nil
	item.Width, item.Height = 3000, 3000
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.makePreview(entry); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("oversized decode was not rejected early: %v", err)
	}
	data, err := s.Preview(entry.ID)
	if err != nil || !bytes.Equal(data, item.Thumbnail) {
		t.Fatal("large legacy image lost its thumbnail fallback")
	}
}

func TestCorruptPreviewCacheRegeneratesFromOriginal(t *testing.T) {
	for _, corrupt := range []string{"truncated", "bad checksum", "oversized header"} {
		t.Run(corrupt, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			item := mustPNG(t)
			entry, err := s.Add(DirLocal, "", item)
			if err != nil {
				t.Fatal(err)
			}
			broken := bytes.Clone(item.Preview)
			switch corrupt {
			case "truncated":
				broken = broken[:33] // Complete IHDR, missing image data.
			case "bad checksum":
				broken[len(broken)-1] ^= 0xff
			case "oversized header":
				binary.BigEndian.PutUint32(broken[16:20], 20000)
				binary.BigEndian.PutUint32(broken[20:24], 20000)
				binary.BigEndian.PutUint32(broken[29:33], crc32.ChecksumIEEE(broken[12:29]))
			}
			path := filepath.Join(dir, "previews", entry.ID+".png")
			if err := os.WriteFile(path, broken, 0o600); err != nil {
				t.Fatal(err)
			}
			img, err := s.PreviewImage(entry.ID)
			if err != nil || img.Bounds().Dx() != item.Width || img.Bounds().Dy() != item.Height {
				t.Fatalf("cache did not recover: %v", err)
			}
			// The repaired cache must also work without the original.
			if err := os.Remove(filepath.Join(dir, "blobs", entry.ImageFile)); err != nil {
				t.Fatal(err)
			}
			data, err := s.Preview(entry.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := png.Decode(bytes.NewReader(data)); err != nil {
				t.Fatalf("repaired cache is not a complete PNG: %v", err)
			}
		})
	}
}

func TestCorruptPreviewFallsBackToThumbnailWhenOriginalUnavailable(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	item := mustPNG(t)
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "previews", entry.ID+".png")
	if err := os.WriteFile(path, item.Preview[:33], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "blobs", entry.ImageFile)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		data, err := s.Preview(entry.ID)
		if err != nil || !bytes.Equal(data, item.Thumbnail) {
			t.Fatalf("thumbnail fallback failed: %v", err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unrecoverable corrupt cache was retained")
	}
}

func BenchmarkCachedPreview(b *testing.B) {
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	var raw bytes.Buffer
	_ = png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 1791, 438)))
	item, err := clip.FromPNG(raw.Bytes())
	if err != nil {
		b.Fatal(err)
	}
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := s.Preview(entry.ID); err != nil {
			b.Fatal(err)
		}
	}
}
