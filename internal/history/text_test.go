package history

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

func TestLongTextRoundTripAndCleanup(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := " \n" + strings.Repeat("你好\n", 200000) + "尾部内容 \n"
	item, err := clip.FromText(body)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.Add(DirLocal, "Mac", item)
	if err != nil {
		t.Fatal(err)
	}
	if entry.TextFile == "" || len(entry.Text) > maxInlineText || Title(entry) != Title(Entry{Text: item.Text}) {
		t.Fatal("body retained in index or title changed")
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	back, err := reopened.Item(entry.ID)
	if err != nil || back.Text != item.Text || back.Hash != item.Hash {
		t.Fatal("full text changed")
	}
	back, err = reopened.PasteItem(entry.ID)
	if err != nil || back.Text != item.Text {
		t.Fatal("long history could not be pasted")
	}
	if err := reopened.SetPinned(entry.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Item(entry.ID); err != nil {
		t.Fatal("clear removed pinned text")
	}
	if err := reopened.Delete(entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "texts", entry.TextFile)); !os.IsNotExist(err) {
		t.Fatal("text body not removed")
	}
}

func TestLegacyMigrationPreservesFullText(t *testing.T) {
	dir := t.TempDir()
	entry := Entry{ID: "legacy", Kind: "text", Text: strings.Repeat("旧正文\n", 10000), At: time.Now().UTC(), Pinned: true, SourceApp: "Editor"}
	old, err := json.Marshal(struct {
		Items []Entry `json:"items"`
	}{[]Entry{entry}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "index.json")
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	// Refuse to replace the index when publishing the migrated metadata fails.
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("migration should fail")
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(unchanged, old) {
		t.Fatal("failed migration changed old data")
	}
	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := s.List()[0]
	if got.TextFile == "" || !got.Pinned || got.SourceApp != entry.SourceApp || got.At != entry.At {
		t.Fatal("metadata changed")
	}
	back, err := s.Item(entry.ID)
	if err != nil || back.Text != entry.Text {
		t.Fatal("migration lost full text")
	}
	again, err := Open(dir)
	if err != nil || len(again.List()[0].Text) > maxInlineText {
		t.Fatal("reload retained full body")
	}
}

func TestHistoryMutationFailureKeepsBodies(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, _ := clip.FromText(strings.Repeat("long text ", 1000))
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(s.journalPath(), s.journalPath()+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.journalPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func() error{
		func() error { return s.SetPinned(entry.ID, true) },
		func() error { return s.Delete(entry.ID) },
		func() error { return s.Clear() },
		func() error { _, err := s.Add(DirLocal, "", item); return err },
	} {
		if err := mutate(); err == nil {
			t.Fatal("mutation should fail")
		}
		list := s.List()
		if len(list) != 1 || list[0].ID != entry.ID || list[0].Pinned {
			t.Fatal("failed mutation changed memory")
		}
		back, err := s.Item(entry.ID)
		if err != nil || back.Text != item.Text {
			t.Fatal("failed mutation removed body")
		}
	}
	files, err := os.ReadDir(filepath.Join(s.dir, "texts"))
	if err != nil || len(files) != 1 {
		t.Fatal("failed add left an orphan body")
	}
}

func TestLongTextAgeCleanup(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, _ := clip.FromText(strings.Repeat("old ", 1000))
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	s.entries[0].At = time.Now().Add(-MaxAge - time.Hour)
	if _, err := s.Add(DirLocal, "", item); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "texts", entry.TextFile)); !os.IsNotExist(err) {
		t.Fatal("expired body not removed")
	}
}

func TestStreamSearchBoundariesAndFields(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// These positions split UTF-8 and ASCII matches across the read boundary.
	cases := []struct {
		body, query string
		want        bool
	}{
		{strings.Repeat("A", textSearchChunk+4*len("你好")+4-1) + "你好 ending", "你好", true},
		{strings.Repeat("A", textSearchChunk+4*len("needle")+4-2) + "nEeDlE ending", "needle", true},
		{strings.Repeat("A", textSearchChunk+4*len("�")+4-1) + "你好 ending", "�", false},
		{strings.Repeat("A", 1000) + "lastword", "lastword mac", true},
		{strings.Repeat("A", 1000) + "lastword", "… mac", false},
		{strings.Repeat("A", 1000) + "lastword", "mac editor", true},
		{strings.Repeat("A", 1000) + "lastword", "editor aaaa", true},
	}
	for i, tc := range cases {
		if err := s.Clear(); err != nil {
			t.Fatal(err)
		}
		item, _ := clip.FromText(tc.body)
		item.SourceApp = "Editor"
		if _, err := s.Add(DirLocal, "Mac", item); err != nil {
			t.Fatal(err)
		}
		got, err := s.Search(context.Background(), tc.query)
		if err != nil || (len(got) == 1) != tc.want {
			t.Fatalf("case %d query %q: %d results, err %v", i, tc.query, len(got), err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Search(ctx, "absent"); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestLegacyIndexFormatsAndTextPaths(t *testing.T) {
	for _, raw := range []string{"", " \n ", "null", "{}", "{\"items\":null}", "{\"items\":[]}"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "index.json"), []byte(raw), 0o600)
		if _, err := Open(dir); err != nil {
			t.Fatalf("valid old index %q: %v", raw, err)
		}
	}
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../outside", "..\\outside", ".", "..", ""} {
		if _, err := s.textPath(name); err == nil {
			t.Fatalf("unsafe path %q", name)
		}
	}
}

func TestTitleTruncatedOnlyWhenListDropsText(t *testing.T) {
	if TitleTruncated(Entry{Text: "短文本"}) || TitleTruncated(Entry{Text: "你好\n世界"}) ||
		TitleTruncated(Entry{Kind: string(clip.KindImage), Text: strings.Repeat("图", 100)}) {
		t.Fatal("complete text or an image was treated as truncated")
	}
	if !TitleTruncated(Entry{Text: strings.Repeat("文", 81)}) || !TitleTruncated(Entry{Text: "短", TextFile: "x.txt"}) {
		t.Fatal("cut or offloaded text was treated as complete")
	}
}

func BenchmarkOpenOffloadedHistory(b *testing.B) {
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	body := strings.Repeat("A", 64<<10)
	for i := range 1000 {
		e := Entry{ID: fmt.Sprint(i), Kind: "text", Text: body, At: time.Now().UTC()}
		if err := s.offloadText(&e); err != nil {
			b.Fatal(err)
		}
		s.entries = append(s.entries, e)
	}
	if err := s.saveLocked(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := Open(s.dir); err != nil {
			b.Fatal(err)
		}
	}
}
