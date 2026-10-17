package clip

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestDecodeUTF16LimitedStopsBeforeAllocatingOutput(t *testing.T) {
	units := utf16.Encode([]rune(strings.Repeat("字", 8)))
	raw := make([]byte, len(units)*2+2)
	for i, u := range units {
		raw[i*2] = byte(u)
		raw[i*2+1] = byte(u >> 8)
	}
	got, err := decodeUTF16Limited(raw, 6)
	if err == nil || got != "" {
		t.Fatalf("got %q %v", got, err)
	}
	text, err := decodeUTF16Limited(raw, 64)
	if err != nil || text != strings.Repeat("字", 8) {
		t.Fatalf("got %q %v", text, err)
	}
}
