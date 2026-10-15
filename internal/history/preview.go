package history

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"

	"github.com/2fa0/snapover/internal/clip"
)

// Old records lack a preview. Limit their one-time original decode to at most
// 32 MiB of pixels (including 16-bit RGBA PNGs); larger ones use the list thumb.
const maxPreviewSourcePixels = 4_000_000

// Preview returns a bounded, detailed PNG without caching original pixels.
// New records reuse the preview made during clipboard decoding. Older records
// generate it on demand and keep only the encoded result on disk.
func (s *Store) Preview(id string) ([]byte, error) {
	data, _, err := s.preview(id)
	return data, err
}

// PreviewImage reuses the decode that validates the cache before displaying it.
func (s *Store) PreviewImage(id string) (image.Image, error) {
	_, img, err := s.preview(id)
	return img, err
}

func (s *Store) preview(id string) ([]byte, image.Image, error) {
	s.mu.Lock()
	entry, ok := s.findLocked(id)
	if !ok || entry.Kind != string(clip.KindImage) || entry.ImageFile == "" {
		s.mu.Unlock()
		return nil, nil, os.ErrNotExist
	}
	path := filepath.Join(s.dir, "previews", entry.ID+".png")
	f, err := os.Open(path)
	var data []byte
	if err == nil {
		data, err = io.ReadAll(io.LimitReader(f, 4<<20))
		f.Close()
	}
	s.mu.Unlock()
	if err == nil {
		if img, decodeErr := decodePreview(data); decodeErr == nil {
			return data, img, nil
		}
	}
	// A valid header can still precede a truncated or corrupt PNG body.
	s.mu.Lock()
	_ = os.Remove(path)
	delete(s.contentSizes, entry.ID)
	s.mu.Unlock()
	data, err = s.makePreview(entry)
	if err != nil {
		data, err = s.Thumb(id)
		if err != nil {
			return nil, nil, err
		}
		img, err := decodePreview(data)
		return data, img, err
	}
	s.mu.Lock()
	// Deleting a record during decoding must not leave a new orphaned cache.
	if _, exists := s.findLocked(id); exists {
		var used int64
		measurable := true
		for _, existing := range s.entries {
			size, err := s.entryBytesLocked(existing)
			if err != nil {
				measurable = false
				break
			}
			used += size
		}
		if measurable && (s.capacity <= 0 || used+int64(len(data)) <= s.capacity) {
			_ = os.WriteFile(path, data, 0o600)
			delete(s.contentSizes, entry.ID)
		}
	}
	s.mu.Unlock()
	img, err := decodePreview(data)
	return data, img, err
}

func decodePreview(data []byte) (image.Image, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > clip.PreviewWidth || cfg.Height > clip.PreviewHeight {
		return nil, os.ErrInvalid
	}
	return png.Decode(bytes.NewReader(data))
}

func (s *Store) makePreview(entry Entry) ([]byte, error) {
	f, err := os.Open(filepath.Join(s.dir, "blobs", entry.ImageFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > clip.MaxImageBytes {
		return nil, os.ErrInvalid
	}
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 20000 || cfg.Height > 20000 || uint64(cfg.Width)*uint64(cfg.Height) > maxPreviewSourcePixels {
		return nil, os.ErrInvalid
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	// Stream the PNG rather than keeping another original-sized byte slice.
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	return clip.PreviewPNG(img)
}
