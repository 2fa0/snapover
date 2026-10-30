package history

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
)

func TestPinKeysRemainBoundAcrossJournalSnapshotAndDeletion(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for n := 0; n < 10; n++ {
		item, _ := clip.FromText("常用片段")
		entry, err := s.Add(DirLocal, "", item)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, entry.ID)
		if err := s.SetPinned(entry.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	assertKeys := func(store *Store) {
		t.Helper()
		for _, entry := range store.List() {
			for i, id := range ids {
				want := i + 1
				if want > 9 {
					want = 0
				}
				if entry.ID == id && entry.PinKey != want {
					t.Fatalf("pin %d has key %d, want %d", i, entry.PinKey, want)
				}
			}
		}
	}
	assertKeys(s)
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	assertKeys(reopened)
	if err := reopened.saveLocked(); err != nil {
		t.Fatal(err)
	}
	reopened, err = Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	assertKeys(reopened)
	if err := reopened.Delete(ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := reopened.SetPinned(ids[9], true); err != nil {
		t.Fatal(err)
	}
	for _, entry := range reopened.List() {
		if entry.ID == ids[9] && entry.PinKey != 1 {
			t.Fatal("freed key was not reused")
		}
		if entry.ID == ids[1] && entry.PinKey != 2 {
			t.Fatal("deletion changed another record's key")
		}
	}
}

func TestLegacyPinsAcquirePersistentKeysWithoutChangingBodies(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.entries = []Entry{{ID: "old", Kind: "text", Text: "原始内容\n", Pinned: true}}
	if err := s.saveLocked(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0]; got.PinKey != 1 || got.Text != "原始内容\n" {
		t.Fatalf("migration: %+v", got)
	}
	s, err = Open(s.dir)
	if err != nil || s.List()[0].PinKey != 1 {
		t.Fatal("assignment did not survive restart", err)
	}
	if err := s.SetPinned("old", false); err != nil {
		t.Fatal(err)
	}
	if s.List()[0].PinKey != 0 {
		t.Fatal("unpin retained its binding")
	}
}

func TestPinKeySaveFailureRollsBack(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, _ := clip.FromText("rollback")
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.journalPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.journalPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(entry.ID, true); err == nil {
		t.Fatal("write failure accepted")
	}
	if got := s.List()[0]; got.Pinned || got.PinKey != 0 {
		t.Fatalf("failed pin remained visible: %+v", got)
	}
	data, err := json.Marshal(SearchPreview(entry, "back"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if _, ok := persisted["MatchText"]; ok {
		t.Fatal("search decoration entered persistent format")
	}
}
