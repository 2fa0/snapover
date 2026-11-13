package settings

import (
	"encoding/json"
	"strings"
)

// ExcludedApp is one entry of the application list. ID is the bundle
// identifier on macOS or the executable path on Windows; Name is what the
// list shows. A rule that has no ID matches the app by name alone, which is
// what the setting held before it had identifiers.
type ExcludedApp struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	// Off keeps the row in the list without letting it decide anything, so a
	// rule can be silenced and kept for later.
	Off bool `json:"off,omitempty"`
}

// ExcludedApps reads both this shape and the list of names the setting used
// to be, so an older settings.json keeps its rules.
type ExcludedApps []ExcludedApp

// UnmarshalJSON accepts the names of the older setting.
func (l *ExcludedApps) UnmarshalJSON(buf []byte) error {
	if entries := []ExcludedApp{}; json.Unmarshal(buf, &entries) == nil {
		*l = ExcludedApps(entries)
		return nil
	}
	var names []string
	if err := json.Unmarshal(buf, &names); err != nil {
		return err
	}
	entries := make(ExcludedApps, 0, len(names))
	for _, name := range names {
		entries = append(entries, ExcludedApp{Name: name})
	}
	*l = entries
	return nil
}

// Active lists the entries that still decide anything, in list order.
func (l ExcludedApps) Active() ExcludedApps {
	active := make(ExcludedApps, 0, len(l))
	for _, entry := range l {
		if !entry.Off && strings.TrimSpace(entry.Name) != "" {
			active = append(active, entry)
		}
	}
	return active
}

// Clean trims what the settings page added and drops rows and repeats that
// say nothing: the same app listed twice would show two switches for one rule.
func (l ExcludedApps) Clean() ExcludedApps {
	cleaned := make(ExcludedApps, 0, len(l))
	for _, entry := range l {
		entry.ID = strings.TrimSpace(entry.ID)
		entry.Name = strings.TrimSpace(entry.Name)
		if entry.Name == "" {
			continue
		}
		duplicate := false
		for _, kept := range cleaned {
			if sameApp(kept, entry) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			cleaned = append(cleaned, entry)
		}
	}
	return cleaned
}

// Find reports where the list already holds the app, or -1.
func (l ExcludedApps) Find(app ExcludedApp) int {
	for i, entry := range l {
		if sameApp(entry, app) {
			return i
		}
	}
	return -1
}

// sameApp matches on the identifier when both entries have one, which keeps
// two apps that share a display name apart.
func sameApp(a, b ExcludedApp) bool {
	if a.ID != "" && b.ID != "" {
		return strings.EqualFold(a.ID, b.ID)
	}
	return strings.EqualFold(a.Name, b.Name)
}
