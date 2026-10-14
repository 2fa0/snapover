package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/history"
)

func TestEntryTitleBoundedAndUnicode(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{" \n你好\n世界 \n", "你好 世界"}, {" \n ", "空文本"},
		{strings.Repeat("中", 80), strings.Repeat("中", 80)},
		{strings.Repeat("中", 81), strings.Repeat("中", 80) + "…"},
		{strings.Repeat("A", 1<<20), strings.Repeat("A", 80) + "…"},
	} {
		if got := entryTitle(history.Entry{Text: tc.text}); got != tc.want {
			t.Fatalf("title %q, want %q", got, tc.want)
		}
	}
}

func TestSearchFieldsAndRefresh(t *testing.T) {
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &app{hist: store, entries: []history.Entry{{ID: "old", Text: "Hello\nWorld", Peer: "Mac", SourceApp: "微信"}}}
	for _, q := range []string{"hello", "WORLD", "hello world", "world mac", "mac 微信", "微信 hello"} {
		if len(a.filtered(q)) != 1 {
			t.Fatalf("no match for %q", q)
		}
	}
	if len(a.filtered("missing")) != 0 {
		t.Fatal("unexpected match")
	}
	// Refresh must release cached entries and stop returning stale results.
	a.filtered("hello")
	a.thumb("old")
	a.refreshEntries()
	if len(a.filtered("hello")) != 0 || a.thumbs.Contains("old") {
		t.Fatal("stale search/thumbnail cache")
	}
}

func TestThumbnailCacheBounded(t *testing.T) {
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &app{hist: store}
	for i := range 600 {
		a.thumb(fmt.Sprint(i))
		waitThumbnails(t, a)
	}
	if a.thumbs.Len() != maxThumbCache || a.thumbs.Contains("0") || !a.thumbs.Contains("599") {
		t.Fatal("cache did not evict oldest entries")
	}
}

func waitThumbnails(t *testing.T, a *app) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for a.thumbActive > 0 || len(a.thumbQueue) > 0 {
		a.drainThumbnails()
		if time.Now().After(deadline) {
			t.Fatal("thumbnail loads did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func BenchmarkHistorySearch(b *testing.B) {
	a := &app{}
	for range 1000 {
		a.entries = append(a.entries, history.Entry{Text: strings.Repeat("A", 16<<10), Peer: "mac"})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		a.pickerSearch.reset()
		a.filtered("missing")
	}
}

func BenchmarkHistorySearchCached(b *testing.B) {
	a := &app{entries: []history.Entry{{Text: strings.Repeat("A", 1<<20)}}}
	a.filtered("missing")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		a.filtered("missing")
	}
}

func BenchmarkEntryTitleLarge(b *testing.B) {
	e := history.Entry{Text: strings.Repeat("A", 1<<20)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		entryTitle(e)
	}
}

func BenchmarkPickerInteraction(b *testing.B) {
	a := &app{pickSel: 0}
	a.pickList.Selected = &a.pickSel
	for i := range 1000 {
		a.entries = append(a.entries, history.Entry{ID: fmt.Sprint(i), Kind: "text", Text: "测试剪贴板历史内容", At: time.Now()})
	}
	tt := ui.NewTester(a.pickerView, 380, 440)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		tt.Key(0, ui.KeyDown)
	}
}
