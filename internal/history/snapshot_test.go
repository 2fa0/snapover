package history

import (
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

func TestListDoesNotWaitForStorageLockAndReturnsIndependentMetadata(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, _ := clip.FromText("visible while storage is busy")
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	done := make(chan []Entry, 1)
	go func() { done <- s.List() }()
	var list []Entry
	select {
	case list = <-done:
	case <-time.After(200 * time.Millisecond):
		s.mu.Unlock()
		<-done
		t.Fatal("list waited for disk writer lock")
	}
	s.mu.Unlock()
	if len(list) != 1 || list[0].ID != entry.ID {
		t.Fatal(list)
	}
	list[0].Text = "changed by caller"
	if s.List()[0].Text != item.Text {
		t.Fatal("caller mutated published history")
	}
	if err := s.Delete(entry.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatal("deleted record stayed published")
	}
}
