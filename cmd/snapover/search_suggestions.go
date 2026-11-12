package main

// The suggestion row under the search field, as Lobe MyGo's Autocomplete
// offers what the typed prefix could mean. Only the names are kept: the
// history entries they come from are already in memory, and a name is a
// string header pointing at the same text.

import (
	"strings"

	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo/ui"
)

const (
	// maxSourceApps bounds what one refresh remembers, newest first.
	maxSourceApps = 24
	// maxSourceSuggestions is how many names the row offers, so that it stays
	// one line of choices rather than a list of its own.
	maxSourceSuggestions = 3
)

// recentSourceApps names the applications the history came from, most recent
// first, once each. It reads the entries the app already holds, so it costs no
// disk and only the headers of the names it keeps.
func recentSourceApps(entries []history.Entry) []string {
	seen := make(map[string]bool, maxSourceApps)
	names := make([]string, 0, maxSourceApps)
	for _, entry := range entries {
		name := strings.TrimSpace(entry.SourceApp)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) == maxSourceApps {
			break
		}
	}
	return names
}

// sourceSuggestions are the applications a query could be short for: those
// whose name contains it, and not the one typed already.
func (a *app) sourceSuggestions(query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var out []string
	for _, name := range a.sourceApps {
		lower := strings.ToLower(name)
		if lower == q || !strings.Contains(lower, q) {
			continue
		}
		out = append(out, name)
		if len(out) == maxSourceSuggestions {
			break
		}
	}
	return out
}

// sourceSuggestionChips offers the applications a query could mean, as
// buttons that search for the whole name. Nothing shows while no name
// matches, so the row costs no room for the queries it cannot help.
func (a *app) sourceSuggestionChips(c *ui.Context, query string, choose func(string)) {
	names := a.sourceSuggestions(query)
	if len(names) == 0 {
		return
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
		for _, name := range names {
			label := a.tr("按来源筛选：") + name
			if ui.Button(c, name).Label(label).Tooltip(label).Height(24).MaxWidth(200).
				FontSize(12).Padding(0, 10).Clicked() {
				choose(name)
				c.Invalidate()
			}
		}
	})
}
