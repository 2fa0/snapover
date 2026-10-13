package history

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/oklog/ulid/v2"
)

const (
	// MaxEntries is how many unpinned items are kept. Pinned items stay.
	MaxEntries = 1000
	// MaxAge is how long an unpinned item is kept.
	MaxAge = 30 * 24 * time.Hour
)

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
	TextFile  string    `json:"text_file,omitempty"`
	Width     int       `json:"width,omitempty"`
	Height    int       `json:"height,omitempty"`
	Size      int       `json:"size,omitempty"`
	Peer      string    `json:"peer,omitempty"`
	ImageFile string    `json:"image,omitempty"`
	ThumbFile string    `json:"thumb,omitempty"`
	Pinned    bool      `json:"pinned,omitempty"`
	SourceApp string    `json:"source_app,omitempty"`
}

// Store keeps the newest clipboard items on disk.
type Store struct {
	mu           sync.Mutex
	dir          string
	entries      []Entry
	sequence     uint64
	journalOps   int
	journalBytes int64
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "thumbs"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "texts"), 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	f, err := os.Open(s.indexPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	migrated := false
	if err == nil {
		migrated, err = s.loadIndex(json.NewDecoder(f))
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	if err := s.replayJournal(); err != nil {
		return nil, err
	}
	if migrated {
		// Publish the new index only after every text body is safely on disk.
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Add stores a text or image item. dir is DirLocal or DirRemote.
func (s *Store) Add(dir, peer string, item clip.Item) (Entry, error) {
	if item.Kind != clip.KindText && item.Kind != clip.KindImage {
		return Entry{}, errors.New("没有可记录的内容")
	}
	id := ulid.MustNew(ulid.Now(), rand.Reader).String()
	entry := Entry{
		ID:        id,
		At:        time.Now(),
		Dir:       dir,
		Kind:      string(item.Kind),
		Text:      item.Text,
		Width:     item.Width,
		Height:    item.Height,
		Size:      item.Size,
		Peer:      peer,
		SourceApp: clip.CleanSource(item.SourceApp),
	}
	if err := s.offloadText(&entry); err != nil {
		return Entry{}, err
	}
	if item.Kind == clip.KindImage {
		entry.ImageFile = id + ".png"
		if err := os.WriteFile(filepath.Join(s.dir, "blobs", entry.ImageFile), item.PNG, 0o600); err != nil {
			return Entry{}, err
		}
		thumb := item.Thumbnail
		if len(thumb) == 0 {
			thumb, _ = thumbPNG(item.PNG, 160)
		}
		if len(thumb) > 0 {
			entry.ThumbFile = id + ".png"
			if err := os.WriteFile(filepath.Join(s.dir, "thumbs", entry.ThumbFile), thumb, 0o600); err != nil {
				entry.ThumbFile = ""
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.entries
	s.entries = append([]Entry{entry}, s.entries...)
	drop := s.trimLocked()
	if err := s.persistLocked(historyChange{Op: "add", Entry: &entry, Drop: entryIDs(drop)}); err != nil {
		s.entries = before
		s.removeFiles([]Entry{entry})
		return Entry{}, err
	}
	s.removeFiles(drop)
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
		if entry.TextFile != "" {
			path, err := s.textPath(entry.TextFile)
			if err != nil {
				return clip.Item{}, err
			}
			f, err := os.Open(path)
			if err != nil {
				return clip.Item{}, err
			}
			defer f.Close()
			raw, err := io.ReadAll(io.LimitReader(f, clip.MaxTextBytes+1))
			if err != nil {
				return clip.Item{}, err
			}
			return clip.FromText(string(raw))
		}
		return clip.FromText(entry.Text)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "blobs", entry.ImageFile))
	if err != nil {
		return clip.Item{}, err
	}
	return clip.FromPNG(data)
}

// Image decodes an original for display without hashing or making another copy.
func (s *Store) Image(id string) (image.Image, error) {
	s.mu.Lock()
	entry, ok := s.findLocked(id)
	s.mu.Unlock()
	if !ok || entry.Kind != string(clip.KindImage) || entry.ImageFile == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "blobs", entry.ImageFile))
	if err != nil {
		return nil, err
	}
	return clip.DecodePNG(data)
}

func (s *Store) findLocked(id string) (Entry, bool) {
	for _, entry := range s.entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return Entry{}, false
}

// SetPinned keeps an item out of age and count cleanup.
func (s *Store) SetPinned(id string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.entries {
		if s.entries[i].ID == id {
			before := s.entries[i].Pinned
			s.entries[i].Pinned = on
			if err := s.persistLocked(historyChange{Op: "pin", ID: id, Pinned: on}); err != nil {
				s.entries[i].Pinned = before
				return err
			}
			return nil
		}
	}
	return os.ErrNotExist
}

// Delete removes one item and its image files.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.entries {
		if s.entries[i].ID != id {
			continue
		}
		before := s.entries
		entry := before[i]
		s.entries = make([]Entry, 0, len(before)-1)
		s.entries = append(s.entries, before[:i]...)
		s.entries = append(s.entries, before[i+1:]...)
		if err := s.persistLocked(historyChange{Op: "drop", Drop: []string{id}}); err != nil {
			s.entries = before
			return err
		}
		s.removeFiles([]Entry{entry})
		return nil
	}
	return os.ErrNotExist
}

// Clear drops every item that is not pinned.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.entries
	var kept []Entry
	var drop []Entry
	for _, entry := range s.entries {
		if entry.Pinned {
			kept = append(kept, entry)
			continue
		}
		drop = append(drop, entry)
	}
	s.entries = kept
	if err := s.persistLocked(historyChange{Op: "drop", Drop: entryIDs(drop)}); err != nil {
		s.entries = before
		return err
	}
	s.removeFiles(drop)
	return nil
}

func (s *Store) trimLocked() []Entry {
	return s.trimAtLocked(time.Now())
}

func (s *Store) trimAtLocked(now time.Time) []Entry {
	kept := make([]Entry, 0, len(s.entries))
	var drop []Entry
	unpinned := 0
	for _, entry := range s.entries {
		if entry.Pinned {
			kept = append(kept, entry)
			continue
		}
		if unpinned >= MaxEntries || now.Sub(entry.At) > MaxAge {
			drop = append(drop, entry)
			continue
		}
		unpinned++
		kept = append(kept, entry)
	}
	s.entries = kept
	return drop
}

func (s *Store) removeFiles(entries []Entry) {
	for _, entry := range entries {
		if entry.TextFile != "" {
			if path, err := s.textPath(entry.TextFile); err == nil {
				_ = os.Remove(path)
			}
		}
		if entry.ImageFile != "" {
			_ = os.Remove(filepath.Join(s.dir, "blobs", entry.ImageFile))
		}
		if entry.ThumbFile != "" {
			_ = os.Remove(filepath.Join(s.dir, "thumbs", entry.ThumbFile))
		}
	}
}

func (s *Store) saveLocked() error {
	tmp := s.indexPath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	writeErr := func() error {
		w := bufio.NewWriter(f)
		if _, err := fmt.Fprintf(w, "{\"sequence\":%d,\"items\":[\n", s.sequence); err != nil {
			return err
		}
		enc := json.NewEncoder(w)
		for i := range s.entries {
			if i > 0 {
				if err := w.WriteByte(','); err != nil {
					return err
				}
			}
			if err := enc.Encode(&s.entries[i]); err != nil {
				return err
			}
		}
		if _, err := w.WriteString("]}\n"); err != nil {
			return err
		}
		return w.Flush()
	}()
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, s.indexPath())
}

func (s *Store) indexPath() string {
	return filepath.Join(s.dir, "index.json")
}

func thumbPNG(data []byte, maxSide int) ([]byte, error) {
	img, err := clip.DecodePNG(data)
	if err != nil {
		return nil, err
	}
	return clip.ThumbnailPNG(img, maxSide)
}
