package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/2fa0/snapover/internal/clip"
)

func TestTextPreviewPreservesContentAndUTF8Boundaries(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		"  首行\n\t缩进内容\n尾部  ",
		strings.Repeat("长文本\n", 200),
		strings.Repeat("A", maxTextPreviewBytes),
		strings.Repeat("A", maxTextPreviewBytes+1),
		strings.Repeat("A", maxTextPreviewBytes-1) + "你好，尾部内容",
		strings.Repeat("A", maxTextPreviewBytes-3) + "你好",
	} {
		item, err := clip.FromText(body)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := s.Add(DirLocal, "", item)
		if err != nil {
			t.Fatal(err)
		}
		text, more, err := s.TextPreview(entry.ID)
		if err != nil || !utf8.ValidString(text) || !strings.HasPrefix(body, text) {
			t.Fatalf("preview changed original content: %v", err)
		}
		if len(text) > maxTextPreviewBytes || more != (len(body) > maxTextPreviewBytes) {
			t.Fatalf("preview size=%d more=%v, body size=%d", len(text), more, len(body))
		}
		if !more && text != body {
			t.Fatal("untruncated preview lost content")
		}
		full, err := s.PasteItem(entry.ID)
		if err != nil || full.Text != body {
			t.Fatal("preview affected full text retrieval")
		}
	}
}

func TestTextPreviewMissingBodyDoesNotFallBackToTitle(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, _ := clip.FromText(strings.Repeat("正文", 1000))
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.dir, "texts", entry.TextFile)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{entry.ID, "missing"} {
		text, _, err := s.TextPreview(id)
		if !os.IsNotExist(err) || text != "" {
			t.Fatalf("missing body should fail instead of displaying a title: %q, %v", text, err)
		}
	}
}
