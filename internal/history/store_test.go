package history

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

func TestAddListAndRestore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	text, err := clip.FromText("你好")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(DirLocal, "", text); err != nil {
		t.Fatal(err)
	}
	img := mustPNG(t)
	added, err := s.Add(DirRemote, "other", img)
	if err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 2 || list[0].ID != added.ID || list[0].Kind != "image" {
		t.Fatalf("list = %+v", list)
	}
	back, err := s.Item(added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Hash != img.Hash || back.Width != img.Width {
		t.Fatalf("restored image mismatch: %+v", back)
	}
	thumb, err := s.Thumb(added.ID)
	if err != nil || len(thumb) == 0 {
		t.Fatalf("thumb: %v len=%d", err, len(thumb))
	}
	again, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.List(); len(got) != 2 || got[1].Text != "你好" {
		t.Fatalf("reload = %+v", got)
	}
}

func TestPinDeleteAndAge(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	keep, err := clip.FromText("keep")
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := s.Add(DirLocal, "", keep)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(pinned.ID, true); err != nil {
		t.Fatal(err)
	}
	old, err := clip.FromText("old")
	if err != nil {
		t.Fatal(err)
	}
	gone, err := s.Add(DirLocal, "", old)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	for i := range s.entries {
		if s.entries[i].ID == gone.ID {
			s.entries[i].At = time.Now().Add(-MaxAge - time.Hour)
		}
	}
	s.trimAtLocked(time.Now())
	_ = s.saveLocked()
	s.mu.Unlock()
	list := s.List()
	if len(list) != 1 || list[0].ID != pinned.ID || !list[0].Pinned {
		t.Fatalf("after age trim: %+v", list)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if n := len(s.List()); n != 1 {
		t.Fatalf("clear removed pinned, len=%d", n)
	}
	if err := s.Delete(pinned.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(s.List()); n != 0 {
		t.Fatalf("delete left %d", n)
	}
}

func mustPNG(t *testing.T) clip.Item {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	img.SetNRGBA(0, 0, color.NRGBA{R: 12, G: 34, B: 56, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromPNG(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return item
}
