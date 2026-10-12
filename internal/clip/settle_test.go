package clip

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWatchCollapsesBurst(t *testing.T) {
	b := &fakeBoard{seq: 1, item: Item{Kind: KindText, Text: "old"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var got []Item
	go watch(ctx, b, 5*time.Millisecond, 40*time.Millisecond, func(_ uint64, item Item) {
		mu.Lock()
		got = append(got, item)
		mu.Unlock()
	})
	time.Sleep(25 * time.Millisecond)
	b.Set(Item{Kind: KindText, Text: "a"})
	time.Sleep(10 * time.Millisecond)
	b.Set(Item{Kind: KindText, Text: "b"})
	time.Sleep(10 * time.Millisecond)
	b.Set(Item{Kind: KindText, Text: "c"})
	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	if len(got) != 1 || got[0].Text != "c" {
		t.Fatalf("burst = %+v", got)
	}
	mu.Unlock()
	b.mu.Lock()
	snaps := b.snaps
	b.mu.Unlock()
	if snaps != 1 {
		t.Fatalf("snapshot count = %d, want 1", snaps)
	}
	b.Set(Item{Kind: KindText, Text: "later"})
	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[1].Text != "later" {
		t.Fatalf("settled copies = %+v", got)
	}
}

func TestCleanSource(t *testing.T) {
	if got := CleanSource("  微信\n"); got != "微信" {
		t.Fatalf("got %q", got)
	}
	if got := CleanSource("a\x00b"); got != "ab" {
		t.Fatalf("control = %q", got)
	}
	long := strings.Repeat("测", 80)
	if got := CleanSource(long); len([]rune(got)) != 64 {
		t.Fatalf("len = %d", len([]rune(got)))
	}
}
