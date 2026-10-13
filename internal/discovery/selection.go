package discovery

import (
	"maps"
	"sort"
)

const modeSelected = "selected"

// Scope is the set of physical segments that may advertise and browse.
// All includes every current and future physical segment.
type Scope struct {
	All  bool
	Keys map[string]bool
}

// Allows reports whether key may be used. An all-scope allows every key.
func (s Scope) Allows(key string) bool {
	if s.All {
		return true
	}
	return s.Keys[key]
}

// Equal compares the selected segments, including whether future ones join.
func (s Scope) Equal(other Scope) bool {
	if s.All || other.All {
		return s.All == other.All
	}
	return maps.Equal(s.Keys, other.Keys)
}

// Selection is the persisted broadcast choice. The zero value uses every
// physical segment, including ones that appear later.
type Selection struct {
	Mode string
	Nets []string
	Seen []string
}

// All reports whether later physical segments join without another edit.
func (s Selection) All() bool { return s.Mode != modeSelected }

// Stopped reports an explicit choice with nothing selected.
func (s Selection) Stopped() bool { return !s.All() && len(s.Nets) == 0 }

// Scope is the filter discovery applies on each rebind.
func (s Selection) Scope() Scope {
	if s.All() {
		return Scope{All: true}
	}
	keys := make(map[string]bool, len(s.Nets))
	for _, key := range s.Nets {
		if key != "" {
			keys[key] = true
		}
	}
	return Scope{Keys: keys}
}

// HasNew reports a current physical segment the last explicit edit did not see
// and did not select. All-mode never reports one, because new segments join.
func (s Selection) HasNew(current []string) bool {
	if s.All() {
		return false
	}
	seen := indexOf(s.Seen)
	nets := indexOf(s.Nets)
	for _, key := range current {
		if key != "" && !seen[key] && !nets[key] {
			return true
		}
	}
	return false
}

// SelectAll restores broadcast and search on every physical segment.
func SelectAll() Selection { return Selection{} }

// SelectNone stops discovery and marks the segments visible at that moment.
func SelectNone(current []string) Selection {
	return Selection{Mode: modeSelected, Seen: uniqSorted(current)}
}

// Remembered restores a saved choice. Any mode other than an explicit
// selection means every physical segment, so older settings keep working.
func Remembered(mode string, nets, seen []string) Selection {
	if mode != modeSelected {
		return SelectAll()
	}
	return Selection{Mode: modeSelected, Nets: uniqSorted(nets), Seen: uniqSorted(seen)}
}

// Toggle moves one segment in or out of the explicit list. Checking every
// current segment does not become all-mode; only SelectAll includes segments
// that appear later. current is the physical segments visible for this edit.
func Toggle(prev Selection, current []string, key string, on bool) Selection {
	if key == "" {
		return prev
	}
	selected := map[string]bool{}
	if prev.All() {
		for _, item := range current {
			if item != "" {
				selected[item] = true
			}
		}
	} else {
		for _, item := range prev.Nets {
			if item != "" {
				selected[item] = true
			}
		}
	}
	if on {
		selected[key] = true
	} else {
		delete(selected, key)
	}
	seen := indexOf(prev.Seen)
	for _, item := range current {
		if item != "" {
			seen[item] = true
		}
	}
	seen[key] = true
	return Selection{Mode: modeSelected, Nets: keysOf(selected), Seen: keysOf(seen)}
}

func indexOf(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		if item != "" {
			out[item] = true
		}
	}
	return out
}

func keysOf(items map[string]bool) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for key := range items {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func uniqSorted(items []string) []string {
	return keysOf(indexOf(items))
}
