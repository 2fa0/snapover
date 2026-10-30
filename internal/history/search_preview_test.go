package history

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/2fa0/snapover/internal/clip"
)

func TestSearchPreviewFindsBodyBeyondTitleAndChunkBoundary(t *testing.T) {
	for _, prefix := range []string{strings.Repeat("说明\n", 100), strings.Repeat("a", textSearchChunk-4)} {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		body := prefix + "K连接错误 Needle 后面的上下文\n" + strings.Repeat("尾", 200)
		item, _ := clip.FromText(body)
		entry, err := s.Add(DirLocal, "电脑", item)
		if err != nil {
			t.Fatal(err)
		}
		for _, query := range []string{"needle", "k连接错误"} {
			results, err := s.Search(context.Background(), query)
			if err != nil || len(results) != 1 {
				t.Fatalf("search %q: %v %v", query, results, err)
			}
			got := results[0]
			if !utf8.ValidString(got.MatchText) || utf8.RuneCountInString(got.MatchText) > 162 || len(got.MatchRanges) == 0 {
				t.Fatalf("invalid snippet: %+v", got)
			}
			for _, r := range got.MatchRanges {
				if r.Start >= r.End || r.End > len(got.MatchText) {
					t.Fatalf("invalid match range: %+v", got)
				}
			}
			if got.MatchField != "" || !strings.Contains(got.MatchText, "连接错误") {
				t.Fatalf("missing body context: %+v", got)
			}
		}
		original, err := s.Item(entry.ID)
		if err != nil || original.Text != body {
			t.Fatal("search changed original content", err)
		}
		if s.List()[0].MatchText != "" {
			t.Fatal("search metadata leaked into store")
		}
	}
}

func TestSearchPreviewExplainsSourceAndFileMatches(t *testing.T) {
	for _, tc := range []struct {
		entry        Entry
		query, field string
	}{
		{Entry{Text: "正文", SourceApp: "Visual Studio Code"}, "studio", "来源应用"},
		{Entry{Text: "正文", Peer: "办公室 Windows"}, "windows", "来源设备"},
		{Entry{Kind: "files", FileName: "报告.pdf"}, "报告", "文件名"},
		{Entry{Kind: "files", FileName: "项目", FileTree: []FilePreviewItem{{Name: "预算.xlsx"}}}, "预算", "目录"},
	} {
		got := SearchPreview(tc.entry, tc.query)
		if got.MatchField != tc.field || got.MatchText == "" || len(got.MatchRanges) == 0 {
			t.Fatalf("%q: %+v", tc.query, got)
		}
	}
}

func TestSearchSnippetMarksEveryMatchInTheWindow(t *testing.T) {
	body := strings.Repeat("前文", 30) + "连接错误 first " + strings.Repeat("中间", 20) + "连接错误 again 连接错误"
	preview, ranges := searchSnippet(body, "连接错误")
	if len(ranges) != 3 {
		t.Fatalf("ranges = %+v, want every match of the window", ranges)
	}
	for i, want := range []string{"连接错误", "连接错误", "连接错误"} {
		if got := preview[ranges[i].Start:ranges[i].End]; got != want {
			t.Fatalf("match %d = %q, want %q", i, got, want)
		}
		if i > 0 && ranges[i].Start < ranges[i-1].End {
			t.Fatalf("ranges overlap: %+v", ranges)
		}
	}
	if !strings.HasPrefix(preview, "…") || !utf8.ValidString(preview) {
		t.Fatalf("snippet = %q", preview)
	}
}

func TestSearchSnippetMarksMatchWhoseCaseDiffersInBytes(t *testing.T) {
	// U+212A KELVIN SIGN is one rune in three bytes, matching a one byte "k".
	preview, ranges := searchSnippet("温度 K与 k 都在", "k")
	if len(ranges) != 2 {
		t.Fatalf("ranges = %+v in %q", ranges, preview)
	}
	for _, r := range ranges {
		if r.End > len(preview) || r.End-r.Start > 3 || r.End-r.Start < 1 {
			t.Fatalf("range %+v does not cover one rune of %q", r, preview)
		}
		if !utf8.ValidString(preview[r.Start:r.End]) {
			t.Fatalf("range %+v splits a rune of %q", r, preview)
		}
	}
}

func TestSearchSnippetBoundsTheMatchesItMarks(t *testing.T) {
	preview, ranges := searchSnippet(strings.Repeat("a", 400), "a")
	if len(ranges) != maxMatchRanges {
		t.Fatalf("ranges = %d, want %d", len(ranges), maxMatchRanges)
	}
	if utf8.RuneCountInString(preview) > matchWindowRunes+1 {
		t.Fatalf("snippet of %d runes was not bounded", utf8.RuneCountInString(preview))
	}
}
