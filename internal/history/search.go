package history

import (
	"context"
	"io"
	"os"
	"unicode/utf8"

	"github.com/charlievieth/strcase/bytcase"
)

const textSearchChunk = 64 << 10

// Search scans on-disk bodies in bounded chunks. Callers may cancel it between
// records and chunks; results contain previews, never full text bodies.
func (s *Store) Search(ctx context.Context, query string) ([]Entry, error) {
	if err := s.errDisabled(); err != nil {
		return nil, err
	}
	entries := s.List()
	if query == "" {
		return entries, nil
	}
	q := []byte(query)
	// Case-equivalent UTF-8 characters can differ in byte length.
	overlap := 4*len(q) + utf8.UTFMax
	var buf []byte
	var out []Entry
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		match := Matches(entry, query)
		if !match && entry.TextFile != "" {
			path, err := s.textPath(entry.TextFile)
			if err != nil {
				return nil, err
			}
			if buf == nil {
				buf = make([]byte, textSearchChunk+overlap)
			}
			found, tail, err := searchTextFile(ctx, path, q, buf, overlap)
			if err != nil {
				if os.IsNotExist(err) {
					s.mu.Lock()
					_, exists := s.findLocked(entry.ID)
					s.mu.Unlock()
					if !exists {
						continue
					}
				}
				return nil, err
			}
			match = found || matchesTail(string(tail), entry, query)
		}
		if match {
			out = append(out, entry)
		}
	}
	return out, nil
}

func searchTextFile(ctx context.Context, path string, q, buf []byte, overlap int) (bool, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, nil, err
	}
	defer f.Close()
	kept := 0
	for {
		if err := ctx.Err(); err != nil {
			return false, nil, err
		}
		n, err := f.Read(buf[kept:])
		data := buf[:kept+n]
		end := len(data)
		if err != io.EOF {
			// An incomplete UTF-8 character at a read boundary belongs to the
			// next chunk, rather than matching a spurious replacement rune.
			start := end - 1
			for start >= max(0, end-utf8.UTFMax) && !utf8.RuneStart(data[start]) {
				start--
			}
			if start >= 0 && !utf8.FullRune(data[start:]) {
				end = start
			}
		}
		if bytcase.Contains(data[:end], q) {
			return true, nil, nil
		}
		if err == io.EOF {
			start := max(0, len(data)-overlap)
			for start < len(data) && !utf8.RuneStart(data[start]) {
				start++
			}
			return false, data[start:], nil
		}
		if err != nil {
			return false, nil, err
		}
		start := max(0, len(data)-overlap)
		for start < len(data) && !utf8.RuneStart(data[start]) {
			start++
		}
		kept = copy(buf, data[start:])
	}
}
