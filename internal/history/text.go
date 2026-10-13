package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/charlievieth/strcase"
	"github.com/2fa0/snapover/internal/clip"
)

// Small text remains inline; long bodies live on disk with only a title in RAM.
const maxInlineText = 512

func (s *Store) textPath(name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return "", errors.New("无效的历史文本文件名")
	}
	return filepath.Join(s.dir, "texts", name), nil
}

func (s *Store) offloadText(entry *Entry) error {
	if entry.Kind != string(clip.KindText) || entry.TextFile != "" || len(entry.Text) <= maxInlineText {
		return nil
	}
	if entry.ID == "" {
		return errors.New("历史文本缺少 ID")
	}
	name := entry.ID + ".txt"
	path, err := s.textPath(name)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Join(s.dir, "texts"), ".text-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := io.WriteString(f, entry.Text)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	entry.TextFile = name
	entry.Text = Title(*entry)
	return nil
}

// loadIndex streams legacy records one at a time, so migration never keeps
// the full JSON file and every decoded text body in memory together.
func (s *Store) loadIndex(dec *json.Decoder) (bool, error) {
	tok, err := dec.Token()
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	migrated := false
	if tok != nil {
		if tok != json.Delim('{') {
			return false, errors.New("无效的历史索引")
		}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return false, err
			}
			if key != "items" {
				if key == "sequence" {
					if err := dec.Decode(&s.sequence); err != nil {
						return false, err
					}
					continue
				}
				var ignored json.RawMessage
				if err := dec.Decode(&ignored); err != nil {
					return false, err
				}
				continue
			}
			tok, err := dec.Token()
			if err != nil {
				return false, err
			}
			if tok == nil {
				continue
			}
			if tok != json.Delim('[') {
				return false, errors.New("无效的历史列表")
			}
			for dec.More() {
				var entry Entry
				if err := dec.Decode(&entry); err != nil {
					return false, err
				}
				inline := entry.TextFile == ""
				if err := s.offloadText(&entry); err != nil {
					return false, err
				}
				migrated = migrated || (inline && entry.TextFile != "")
				s.entries = append(s.entries, entry)
			}
			if _, err := dec.Token(); err != nil {
				return false, err
			}
		}
		if _, err := dec.Token(); err != nil {
			return false, err
		}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("历史索引包含多余内容")
		}
		return false, err
	}
	return migrated, nil
}

// Title describes a record using at most 80 text runes.
func Title(entry Entry) string {
	if entry.Kind == string(clip.KindImage) {
		if entry.Width > 0 {
			return fmt.Sprintf("图片  %d×%d", entry.Width, entry.Height)
		}
		return "图片"
	}
	text := strings.TrimSpace(entry.Text)
	if text == "" {
		return "空文本"
	}
	var title strings.Builder
	title.Grow(80)
	n := 0
	for _, r := range text {
		if n == 80 {
			title.WriteRune('…')
			break
		}
		if r == '\n' {
			r = ' '
		}
		title.WriteRune(r)
		n++
	}
	return title.String()
}

// Matches checks inline text and display fields without copying long bodies.
func Matches(entry Entry, query string) bool {
	if entry.TextFile != "" {
		return strcase.Contains(entry.Peer+" "+entry.SourceApp+" "+Title(entry), query)
	}
	if strcase.Contains(entry.Text, query) {
		return true
	}
	return matchesTail(entry.Text, entry, query)
}

func matchesTail(text string, entry Entry, query string) bool {
	if n := 4 * len(query); len(text) > n {
		text = text[len(text)-n:]
		for len(text) > 0 && !utf8.RuneStart(text[0]) {
			text = text[1:]
		}
	}
	return strcase.Contains(text+" "+entry.Peer+" "+entry.SourceApp+" "+Title(entry), query)
}
