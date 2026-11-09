package history

import (
	"context"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/charlievieth/strcase"
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
		entry = SearchPreview(entry, query)
		// An offloaded title may match, but the preview still needs the
		// original body around that match rather than its truncated title.
		if entry.TextFile != "" && entry.MatchField == "" {
			match = false
		}
		if !match && entry.TextFile != "" {
			path, err := s.textPath(entry.TextFile)
			if err != nil {
				return nil, err
			}
			if buf == nil {
				buf = make([]byte, textSearchChunk+overlap)
			}
			found, preview, tail, err := searchTextFile(ctx, path, q, buf, overlap)
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
			if found {
				// The body is what the row now shows, so a field the snippet
				// replaced must not label it.
				entry.MatchField = ""
				entry.MatchText, entry.MatchRanges = searchSnippet(string(preview), query)
			}
		}
		if match {
			out = append(out, entry)
		}
	}
	return out, nil
}

func searchTextFile(ctx context.Context, path string, q, buf []byte, overlap int) (bool, []byte, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, nil, nil, err
	}
	defer f.Close()
	kept := 0
	for {
		if err := ctx.Err(); err != nil {
			return false, nil, nil, err
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
			return true, data[:end], nil, nil
		}
		if err == io.EOF {
			start := max(0, len(data)-overlap)
			for start < len(data) && !utf8.RuneStart(data[start]) {
				start++
			}
			return false, nil, data[start:], nil
		}
		if err != nil {
			return false, nil, nil, err
		}
		start := max(0, len(data)-overlap)
		for start < len(data) && !utf8.RuneStart(data[start]) {
			start++
		}
		kept = copy(buf, data[start:])
	}
}

// SearchPreview decorates a copy of an entry, without loading its disk body.
func SearchPreview(entry Entry, query string) Entry {
	fields := []struct{ name, text string }{{"", entry.Text}}
	if entry.Pinned && entry.Note != "" {
		fields = append(fields, struct{ name, text string }{"备注", entry.Note})
	}
	fields = append(fields,
		struct{ name, text string }{"来源应用", entry.SourceApp},
		struct{ name, text string }{"来源设备", entry.Peer},
		struct{ name, text string }{"文件名", entry.FileName},
	)
	for _, item := range entry.FileTree {
		fields = append(fields, struct{ name, text string }{"目录", item.Name})
	}
	fields = append(fields, struct{ name, text string }{"类型", Title(entry)})
	for _, field := range fields {
		if query != "" && strcase.Contains(field.text, query) {
			entry.MatchField = field.name
			entry.MatchText, entry.MatchRanges = searchSnippet(field.text, query)
			return entry
		}
	}
	return entry
}

// The snippet keeps the text around the first match, leading into it with a
// short run of context and continuing for at most these runes.
const (
	matchLeadRunes   = 12
	matchWindowRunes = 160
	// maxMatchRanges bounds the marks of one snippet: a one-letter query would
	// otherwise mark every rune of it.
	maxMatchRanges = 24
)

// searchSnippet retains a window of text around the first match, with every
// match inside it marked. Byte offsets refer to the displayed snippet,
// including any leading ellipsis.
func searchSnippet(text, query string) (string, []MatchRange) {
	first := strcase.Index(text, query)
	if first < 0 || query == "" {
		return "", nil
	}
	start := first
	for n := 0; n < matchLeadRunes && start > 0; n++ {
		_, size := utf8.DecodeLastRuneInString(text[:start])
		start -= size
	}
	end := start
	for n := 0; n < matchWindowRunes && end < len(text); n++ {
		_, size := utf8.DecodeRuneInString(text[end:])
		end += size
	}
	ranges := matchRanges(text[start:end], query)
	// The replacements are one byte for one byte, so the offsets still hold.
	preview := strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(text[start:end])
	if start > 0 {
		const ellipsis = "…"
		preview = ellipsis + preview
		for i := range ranges {
			ranges[i].Start += len(ellipsis)
			ranges[i].End += len(ellipsis)
		}
	}
	if end < len(text) {
		preview += "…"
	}
	return preview, ranges
}

// matchRanges marks every non-overlapping match of query in text, up to
// maxMatchRanges of them.
func matchRanges(text, query string) []MatchRange {
	var ranges []MatchRange
	for at := 0; at < len(text) && len(ranges) < maxMatchRanges; {
		offset := strcase.Index(text[at:], query)
		if offset < 0 {
			break
		}
		start := at + offset
		// Case-equivalent characters can differ in byte length, so the end
		// counts the query's runes in the text rather than its bytes.
		end := start
		for range query {
			if end >= len(text) {
				break
			}
			_, size := utf8.DecodeRuneInString(text[end:])
			end += size
		}
		if end <= start {
			break
		}
		ranges = append(ranges, MatchRange{Start: start, End: end})
		at = end
	}
	return ranges
}
