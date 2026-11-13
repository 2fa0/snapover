//go:build darwin

package apps

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A name is read from a string whose buffer the autorelease pool owns: the
// list shows it long after the pool drained, so it has to be a copy, and one
// that a label can be drawn with.
func TestRunningAppsHaveUsableNames(t *testing.T) {
	list := Running()
	if len(list) == 0 {
		t.Skip("no app is running with a user interface")
	}
	for _, entry := range list {
		if entry.ID == "" {
			t.Fatalf("%q has no identifier", entry.Name)
		}
		if entry.Name == "" {
			t.Fatalf("%s has no name", entry.ID)
		}
		if !utf8.ValidString(entry.Name) {
			t.Fatalf("%s has an invalid name: %q", entry.ID, entry.Name)
		}
		if strings.IndexFunc(entry.Name, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			t.Fatalf("%s has a name a label cannot show: %q", entry.ID, entry.Name)
		}
	}
}

func TestPrintableDropsControlCharacters(t *testing.T) {
	if got := printable("\x00\x00\x00"); got != "" {
		t.Fatalf("printable kept control characters: %q", got)
	}
	if got := printable("访达"); got != "访达" {
		t.Fatalf("printable changed a name: %q", got)
	}
}
