package history

import (
	"io"
	"os"
	"unicode/utf8"

	"github.com/2fa0/snapover/internal/clip"
)

const maxTextPreviewBytes = 16 << 10

// TextPreview reads a bounded prefix; full bodies stay on disk until requested.
func (s *Store) TextPreview(id string) (text string, more bool, err error) {
	if err := s.errDisabled(); err != nil {
		return "", false, err
	}
	s.mu.Lock()
	entry, ok := s.findLocked(id)
	s.mu.Unlock()
	if !ok || entry.Kind != string(clip.KindText) {
		return "", false, os.ErrNotExist
	}
	text = entry.Text
	if entry.TextFile != "" {
		path, err := s.textPath(entry.TextFile)
		if err != nil {
			return "", false, err
		}
		f, err := os.Open(path)
		if err != nil {
			return "", false, err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxTextPreviewBytes+1))
		if err != nil {
			return "", false, err
		}
		text = string(data)
	}
	if len(text) <= maxTextPreviewBytes {
		return text, false, nil
	}
	end := maxTextPreviewBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], true, nil
}
