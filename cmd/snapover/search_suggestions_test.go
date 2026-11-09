package main

import (
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo/ui"
)

func TestRecentSourceAppsKeepsTheLatestNamesOnce(t *testing.T) {
	entries := []history.Entry{
		{SourceApp: "Google Chrome"},
		{SourceApp: "飞书"},
		{SourceApp: ""},
		{SourceApp: "Google Chrome"},
		{SourceApp: "  "},
		{SourceApp: "Xcode"},
	}
	got := recentSourceApps(entries)
	want := []string{"Google Chrome", "飞书", "Xcode"}
	if len(got) != len(want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sources = %v, want %v", got, want)
		}
	}
	if len(recentSourceApps(nil)) != 0 {
		t.Fatal("an empty history named a source")
	}
}

func TestRecentSourceAppsStopsAtItsBound(t *testing.T) {
	var entries []history.Entry
	for i := range maxSourceApps + 10 {
		entries = append(entries, history.Entry{SourceApp: "app-" + string(rune('a'+i%26)) + strings.Repeat("x", i)})
	}
	if got := recentSourceApps(entries); len(got) > maxSourceApps {
		t.Fatalf("kept %d names, over the %d bound", len(got), maxSourceApps)
	}
}

func TestSourceSuggestionsMatchWhatWasTyped(t *testing.T) {
	a := &app{sourceApps: []string{"Google Chrome", "Chrome Remote Desktop", "飞书", "Xcode"}}
	for _, test := range []struct {
		query string
		want  []string
	}{
		{"chr", []string{"Google Chrome", "Chrome Remote Desktop"}},
		{"CHROME", []string{"Google Chrome", "Chrome Remote Desktop"}},
		{"飞", []string{"飞书"}},
		{"", nil},
		{"   ", nil},
		{"safari", nil},
	} {
		got := a.sourceSuggestions(test.query)
		if len(got) != len(test.want) {
			t.Fatalf("%q suggested %v, want %v", test.query, got, test.want)
		}
		for i := range test.want {
			if got[i] != test.want[i] {
				t.Fatalf("%q suggested %v, want %v", test.query, got, test.want)
			}
		}
	}
	// Exactly what was typed is no suggestion, or the row would never close.
	if got := a.sourceSuggestions("飞书"); len(got) != 0 {
		t.Fatalf("the typed name was suggested back: %v", got)
	}
}

func TestSourceSuggestionsOfferAtMostThree(t *testing.T) {
	a := &app{sourceApps: []string{"Code One", "Code Two", "Code Three", "Code Four"}}
	if got := a.sourceSuggestions("code"); len(got) != maxSourceSuggestions {
		t.Fatalf("offered %d names, want %d", len(got), maxSourceSuggestions)
	}
}

func suggestionTestApp(t *testing.T) *app {
	t.Helper()
	a := interfaceTestApp(t)
	for _, source := range []string{"Google Chrome", "飞书"} {
		item, _ := clip.FromText("来自 " + source + " 的一段文本")
		item.SourceApp = source
		if _, err := a.hist.Add(history.DirLocal, "", item); err != nil {
			t.Fatal(err)
		}
	}
	a.refreshEntries()
	return a
}

func TestTypingASourceOffersItAndChoosingItSearchesForIt(t *testing.T) {
	a := suggestionTestApp(t)
	if got := a.sourceApps; len(got) != 2 {
		t.Fatalf("history sources = %v", got)
	}
	a.page = pageHistory
	tt := ui.NewTester(a.view, 960, 640)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	clickInterface(t, tt, "搜索复制历史")
	tt.Type("chr")
	if !tt.HasText("Google Chrome") {
		t.Fatal("typing a source name offered nothing")
	}
	clickInterface(t, tt, "按来源筛选：Google Chrome")
	if a.query != "Google Chrome" {
		t.Fatalf("choosing the source searched for %q", a.query)
	}
	if tt.HasText("按来源筛选：Google Chrome") {
		t.Fatal("the row kept offering the name it just searched for")
	}
	if !tt.HasText("来自 Google Chrome 的一段文本") || tt.HasText("来自 飞书 的一段文本") {
		t.Fatal("the chosen source did not become the search")
	}
}

func TestQuickHistoryOffersAndSearchesASource(t *testing.T) {
	a := suggestionTestApp(t)
	tt := ui.NewTester(a.pickerView, 380, 440)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	clickInterface(t, tt, "搜索快速历史")
	tt.Type("飞")
	if !tt.HasText("飞书") {
		t.Fatal("the picker offered no source")
	}
	clickInterface(t, tt, "按来源筛选：飞书")
	if a.pickQuery != "飞书" {
		t.Fatalf("the picker searched for %q", a.pickQuery)
	}
	if !tt.HasText("来自 飞书 的一段文本") || tt.HasText("来自 Google Chrome 的一段文本") {
		t.Fatal("the chosen source did not filter the quick history")
	}
}
