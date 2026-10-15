package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
)

func TestCapacityKeepsPinsAndNewestEntries(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	for _, value := range []string{"pinned", "oldest", "middle", "newest"} {
		item, _ := clip.FromText(value)
		entry, err := s.Add(DirLocal, "", item)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	if err := s.SetPinned(entries[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCapacity(18); err != nil {
		t.Fatal(err)
	}
	got := s.List()
	if len(got) != 3 || got[0].ID != entries[3].ID || got[1].ID != entries[2].ID || !got[2].Pinned {
		t.Fatalf("capacity kept wrong entries: %+v", got)
	}
	// Subsequent copies enforce the limit too, without evicting pins.
	item, _ := clip.FromText("latest")
	latest, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	got = s.List()
	if len(got) != 3 || got[0].ID != latest.ID || got[1].ID != entries[3].ID || !got[2].Pinned {
		t.Fatalf("new copy: %+v", got)
	}
	again, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored := again.List(); len(restored) != len(got) || restored[0].ID != latest.ID || !restored[2].Pinned {
		t.Fatalf("pruning was not durable: %+v", restored)
	}
}

func TestCapacityCountsDiskAssetsAndRemovesThem(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := mustPNG(t)
	item.Preview = item.Thumbnail
	entry, err := s.Add(DirRemote, "peer", item)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := s.Usage()
	if err != nil {
		t.Fatal(err)
	}
	want := int64(len(item.PNG) + len(item.Thumbnail) + len(item.Preview))
	if usage.ContentBytes != want || usage.TotalBytes <= want {
		t.Fatalf("usage=%+v want contents=%d", usage, want)
	}
	if err := s.SetCapacity(want - 1); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatal("oversize image was kept")
	}
	for _, path := range []string{filepath.Join(s.dir, "blobs", entry.ImageFile), filepath.Join(s.dir, "thumbs", entry.ThumbFile), filepath.Join(s.dir, "previews", entry.ID+".png")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("asset was left: %s, %v", path, err)
		}
	}
}

func TestCapacityRejectsUnstorableCopyWithoutDiscardingHistory(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, _ := clip.FromText("keep")
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCapacity(8); err != nil {
		t.Fatal(err)
	}
	tooLarge, _ := clip.FromText(strings.Repeat("x", 1000))
	if _, err := s.Add(DirLocal, "", tooLarge); err == nil {
		t.Fatal("unstorable text reported as recorded")
	}
	if got := s.List(); len(got) != 1 || got[0].ID != entry.ID {
		t.Fatal("unstorable new copy removed existing history")
	}
	files, _ := os.ReadDir(filepath.Join(s.dir, "texts"))
	if len(files) != 0 {
		t.Fatal("rejected copy left its text file")
	}
	if err := s.SetPinned(entry.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCapacity(1); err != nil {
		t.Fatal(err)
	}
	usage, _ := s.Usage()
	if len(s.List()) != 1 || usage.PinnedBytes != 4 {
		t.Fatal("quota removed pin")
	}
	small, _ := clip.FromText("a")
	if _, err := s.Add(DirRemote, "peer", small); err == nil {
		t.Fatal("copy exceeded pinned quota")
	}
}

func TestCapacityFailurePreservesEntriesAndFiles(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.Add(DirLocal, "", mustPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.journalPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.journalPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCapacity(1); err == nil {
		t.Fatal("failed journal write reported as pruning success")
	}
	if len(s.List()) != 1 {
		t.Fatal("failed pruning changed history")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "blobs", entry.ImageFile)); err != nil {
		t.Fatal("failed pruning removed file", err)
	}
}

func TestCapacityCountsOffloadedTextAndSkipsOversizePreviewCache(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, _ := clip.FromText(strings.Repeat("中文", 300))
	entry, err := s.Add(DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	usage, _ := s.Usage()
	if entry.TextFile == "" || usage.ContentBytes != int64(len(item.Text)+len(entry.Text)) {
		t.Fatalf("offloaded text not counted: %+v", usage)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	image := mustPNG(t)
	image.Preview = nil
	entry, err = s.Add(DirLocal, "", image)
	if err != nil {
		t.Fatal(err)
	}
	usage, _ = s.Usage()
	if err := s.SetCapacity(usage.ContentBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Preview(entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "previews", entry.ID+".png")); !os.IsNotExist(err) {
		t.Fatal("on-demand preview exceeded quota")
	}
}

func TestCleanupReportsAndRetriesOrphanFiles(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.Add(DirLocal, "", mustPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, "blobs", entry.ImageFile)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// A nonempty directory deterministically makes Remove fail on both platforms.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(path, "held")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err == nil {
		t.Fatal("asset removal failure was hidden")
	}
	if len(s.List()) != 0 {
		t.Fatal("durably dropped entry was retained")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	// Simulate restarting after the durable drop but before asset deletion.
	again, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(s.dir, "blobs", "personal.png")
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := again.SetCapacity(DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("orphan not retried: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("foreign file removed", err)
	}
}

func TestUsageRefreshAndInvalidAssetDoNotSilentlyUndercount(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.Add(DirLocal, "", mustPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Usage()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, "blobs", entry.ImageFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write([]byte("changed"))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.Usage()
	if err != nil || after.ContentBytes != before.ContentBytes+7 {
		t.Fatalf("stale size: %+v %v", after, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Usage(); err == nil {
		t.Fatal("invalid asset was counted as zero")
	}
	capacity := s.capacity
	if err := s.SetCapacity(1); err == nil {
		t.Fatal("unmeasurable quota applied")
	}
	if len(s.List()) != 1 || s.capacity != capacity {
		t.Fatal("failed measurement changed live quota or entries")
	}
}
