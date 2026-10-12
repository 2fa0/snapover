package history

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/2fa0/clipbridge/internal/clip"
	"github.com/oklog/ulid/v2"
)

const MaxEntries = 200

const (
	DirLocal  = "local"
	DirRemote = "remote"
)

// Entry is one clipboard snapshot kept for the history page.
type Entry struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	Dir       string    `json:"dir"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text,omitempty"`
	Width     int       `json:"width,omitempty"`
	Height    int       `json:"height,omitempty"`
	Size      int       `json:"size,omitempty"`
	Peer      string    `json:"peer,omitempty"`
	ImageFile string    `json:"image,omitempty"`
	ThumbFile string    `json:"thumb,omitempty"`
}

// Store keeps the newest clipboard items on disk.
type Store struct {
	mu      sync.Mutex
	dir     string
	entries []Entry
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "thumbs"), 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	buf, err := os.ReadFile(s.indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(buf)) == 0 {
		return s, nil
	}
	var file struct {
		Items []Entry `json:"items"`
	}
	if err := json.Unmarshal(buf, &file); err != nil {
		return nil, err
	}
	s.entries = file.Items
	return s, nil
}

// Add stores a text or image item. dir is DirLocal or DirRemote.
func (s *Store) Add(dir, peer string, item clip.Item) (Entry, error) {
	if item.Kind != clip.KindText && item.Kind != clip.KindImage {
		return Entry{}, errors.New("没有可记录的内容")
	}
	id := ulid.MustNew(ulid.Now(), rand.Reader).String()
	entry := Entry{
		ID:     id,
		At:     time.Now(),
		Dir:    dir,
		Kind:   string(item.Kind),
		Text:   item.Text,
		Width:  item.Width,
		Height: item.Height,
		Size:   item.Size,
		Peer:   peer,
	}
	if item.Kind == clip.KindImage {
		entry.ImageFile = id + ".png"
		if err := os.WriteFile(filepath.Join(s.dir, "blobs", entry.ImageFile), item.PNG, 0o600); err != nil {
			return Entry{}, err
		}
		if thumb, err := thumbPNG(item.PNG, 160); err == nil {
			entry.ThumbFile = id + ".png"
			if err := os.WriteFile(filepath.Join(s.dir, "thumbs", entry.ThumbFile), thumb, 0o600); err != nil {
				entry.ThumbFile = ""
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append([]Entry{entry}, s.entries...)
	s.trimLocked()
	if err := s.saveLocked(); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

func (s *Store) Thumb(id string) ([]byte, error) {
	s.mu.Lock()
	entry, ok := s.findLocked(id)
	s.mu.Unlock()
	if !ok || entry.ThumbFile == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join(s.dir, "thumbs", entry.ThumbFile))
}

// Item rebuilds a clipboard item so the history page can write it back.
func (s *Store) Item(id string) (clip.Item, error) {
	s.mu.Lock()
	entry, ok := s.findLocked(id)
	s.mu.Unlock()
	if !ok {
		return clip.Item{}, os.ErrNotExist
	}
	if entry.Kind == string(clip.KindText) {
		return clip.FromText(entry.Text)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "blobs", entry.ImageFile))
	if err != nil {
		return clip.Item{}, err
	}
	return clip.FromPNG(data)
}

func (s *Store) findLocked(id string) (Entry, bool) {
	for _, entry := range s.entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return Entry{}, false
}

func (s *Store) trimLocked() {
	if len(s.entries) <= MaxEntries {
		return
	}
	drop := s.entries[MaxEntries:]
	s.entries = s.entries[:MaxEntries]
	for _, entry := range drop {
		if entry.ImageFile != "" {
			_ = os.Remove(filepath.Join(s.dir, "blobs", entry.ImageFile))
		}
		if entry.ThumbFile != "" {
			_ = os.Remove(filepath.Join(s.dir, "thumbs", entry.ThumbFile))
		}
	}
}

func (s *Store) saveLocked() error {
	buf, err := json.MarshalIndent(struct {
		Items []Entry `json:"items"`
	}{Items: s.entries}, "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	tmp := s.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.indexPath())
}

func (s *Store) indexPath() string {
	return filepath.Join(s.dir, "index.json")
}

func thumbPNG(data []byte, maxSide int) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 {
		return nil, errors.New("空图片")
	}
	tw, th := w, h
	if w > maxSide || h > maxSide {
		if w >= h {
			th = h * maxSide / w
			tw = maxSide
		} else {
			tw = w * maxSide / h
			th = maxSide
		}
		if tw < 1 {
			tw = 1
		}
		if th < 1 {
			th = 1
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, tw, th))
	for y := 0; y < th; y++ {
		sy := b.Min.Y + y*h/th
		for x := 0; x < tw; x++ {
			sx := b.Min.X + x*w/tw
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
