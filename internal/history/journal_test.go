package history

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
)

func TestJournalRestoresMutationsAndLongBodies(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	text, _ := clip.FromText(strings.Repeat("正文\n", 1000))
	keep, err := s.Add(DirLocal, "Mac", text)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(keep.ID, true); err != nil {
		t.Fatal(err)
	}
	image, err := s.Add(DirRemote, "Windows", mustPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if entries := reopened.List(); len(entries) != 1 || entries[0].ID != keep.ID || !entries[0].Pinned {
		t.Fatalf("replayed records = %+v", entries)
	}
	if item, err := reopened.Item(keep.ID); err != nil || item.Text != text.Text {
		t.Fatal("journal replay lost the long text body")
	}
	if _, err := reopened.Item(image.ID); !os.IsNotExist(err) {
		t.Fatal("cleared image returned after replay")
	}
	if err := reopened.Delete(keep.ID); err != nil {
		t.Fatal(err)
	}
	reopened, err = Open(s.dir)
	if err != nil || len(reopened.List()) != 0 {
		t.Fatal("deletion was not restored")
	}
}

func TestJournalRecoversOnlyIncompleteTail(t *testing.T) {
	for _, tail := range [][]byte{{1, 2, 3}, {10, 0, 0, 0, 0, 0, 0, 0, 'x'}} {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		item, _ := clip.FromText("first")
		if _, err := s.Add(DirLocal, "", item); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(s.journalPath())
		if err := os.WriteFile(s.journalPath(), append(append([]byte{}, before...), tail...), 0o600); err != nil {
			t.Fatal(err)
		}
		reopened, err := Open(s.dir)
		if err != nil || len(reopened.List()) != 1 {
			t.Fatalf("partial tail recovery: %v", err)
		}
		after, _ := os.ReadFile(s.journalPath())
		if !bytes.Equal(before, after) {
			t.Fatal("recovery altered complete records")
		}
		item, _ = clip.FromText("second")
		if _, err := reopened.Add(DirLocal, "", item); err != nil {
			t.Fatal(err)
		}
		again, err := Open(s.dir)
		if err != nil || len(again.List()) != 2 || again.List()[0].Text != "second" {
			t.Fatal("append after recovery was not replayable")
		}
	}
}

func TestJournalRejectsCompleteCorruptionWithoutDiscardingData(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, _ := clip.FromText("keep")
	if _, err := s.Add(DirLocal, "", item); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(s.journalPath())
	raw[len(raw)-1] ^= 1
	if err := os.WriteFile(s.journalPath(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(s.dir); err == nil {
		t.Fatal("corrupt complete record was silently accepted")
	}
	unchanged, _ := os.ReadFile(s.journalPath())
	if !bytes.Equal(raw, unchanged) {
		t.Fatal("recovery discarded the damaged journal")
	}
}

func TestSnapshotSequenceSkipsAlreadyCommittedJournal(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, _ := clip.FromText("keep")
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(entry.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(DirLocal, "", item); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	// Emulate exit between snapshot replacement and journal truncation.
	if err := s.saveLocked(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.dir)
	if err != nil || len(reopened.List()) != 1 || reopened.List()[0].ID != entry.ID {
		t.Fatal("replaying a checkpointed journal duplicated or resurrected records")
	}
}

func TestJournalCheckpointAndRetryAfterFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if fail {
			if err := os.Mkdir(s.indexPath()+".tmp", 0o700); err != nil {
				t.Fatal(err)
			}
		}
		item, _ := clip.FromText("keep")
		entry, err := s.Add(DirLocal, "", item)
		if err != nil {
			t.Fatal(err)
		}
		for i := range checkpointOps - 1 {
			if err := s.SetPinned(entry.ID, i%2 == 0); err != nil {
				t.Fatalf("checkpoint failure must not undo a committed mutation: %v", err)
			}
		}
		info, err := os.Stat(s.journalPath())
		if err != nil || (info.Size() == 0) == fail {
			t.Fatalf("checkpoint fail=%v, journal stat=%v", fail, err)
		}
		reopened, err := Open(s.dir)
		if err != nil || len(reopened.List()) != 1 || !reopened.List()[0].Pinned {
			t.Fatal("checkpoint or its fallback lost the committed pin")
		}
		if fail {
			if err := os.Remove(s.indexPath() + ".tmp"); err != nil {
				t.Fatal(err)
			}
			if err := reopened.SetPinned(entry.ID, false); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Stat(s.journalPath()); err != nil || info.Size() != 0 {
				t.Fatal("checkpoint was not retried after recovery")
			}
		}
	}
}
