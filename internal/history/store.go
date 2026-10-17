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
	listMu       sync.RWMutex
	listed       []Entry
	openedMu     sync.Mutex
	openedClosed bool
	dir          string
	entries      []Entry
	sequence     uint64
	journalOps   int
	journalBytes int64
	capacity     int64
	contentSizes map[string]int64
	disabled     error
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "thumbs"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "previews"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "texts"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "opened"), 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, capacity: DefaultMaxBytes, contentSizes: make(map[string]int64)}
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
	// Staged viewer files belong to the previous process.
	s.cleanOpened()
	s.publishLocked()
	return s, nil
}

// Add stores a text or image item. dir is DirLocal or DirRemote.
func (s *Store) Add(dir, peer string, item clip.Item) (Entry, error) {
	if err := s.errDisabled(); err != nil {
		return Entry{}, err
	}
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
	if item.Kind == clip.KindImage && len(item.Thumbnail) == 0 {
		item.Thumbnail, _ = thumbPNG(item.PNG, 160)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishLocked()
	if err := s.offloadText(&entry); err != nil {
		return Entry{}, err
	}
	if item.Kind == clip.KindImage {
		entry.ImageFile = id + ".png"
		if err := os.WriteFile(filepath.Join(s.dir, "blobs", entry.ImageFile), item.PNG, 0o600); err != nil {
			return Entry{}, errors.Join(err, s.removeFiles([]Entry{entry}))
		}
		thumb := item.Thumbnail
		if len(thumb) > 0 {
			entry.ThumbFile = id + ".png"
			if err := os.WriteFile(filepath.Join(s.dir, "thumbs", entry.ThumbFile), thumb, 0o600); err != nil {
				entry.ThumbFile = ""
			}
		}
		if len(item.Preview) > 0 {
			_ = os.WriteFile(filepath.Join(s.dir, "previews", id+".png"), item.Preview, 0o600)
		}
	}
	if s.capacity > 0 {
		required, err := s.entryBytesLocked(entry)
		if err != nil {
			return Entry{}, errors.Join(err, s.removeFiles([]Entry{entry}))
		}
		for _, existing := range s.entries {
			if existing.Pinned {
				size, err := s.entryBytesLocked(existing)
				if err != nil {
					return Entry{}, errors.Join(err, s.removeFiles([]Entry{entry}))
				}
				required += size
			}
		}
		if required > s.capacity {
			return Entry{}, errors.Join(errors.New("历史容量不足，未保存本条内容，请提高上限或清理置顶记录"), s.removeFiles([]Entry{entry}))
		}
	}
	before := s.entries
	s.entries = append([]Entry{entry}, s.entries...)
	drop, err := s.trimLocked()
	if err != nil {
		s.entries = before
		return Entry{}, errors.Join(err, s.removeFiles([]Entry{entry}))
	}
	if err := s.persistLocked(historyChange{Op: "add", Entry: &entry, Drop: entryIDs(drop)}); err != nil {
		s.entries = before
		return Entry{}, errors.Join(err, s.removeFiles([]Entry{entry}))
	}
	return entry, s.removeFiles(drop)
}

func (s *Store) List() []Entry {
	s.listMu.RLock()
	defer s.listMu.RUnlock()
	out := make([]Entry, len(s.listed))
	copy(out, s.listed)
	return out
}

// Publish only metadata under a separate, short-lived lock. Readers never
// queue behind file writes, fsync, checkpointing or directory cleanup.
func (s *Store) publishLocked() {
	entries := append([]Entry(nil), s.entries...)
	s.listMu.Lock()
	s.listed = entries
	s.listMu.Unlock()
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
	return s.readItem(id, false)
}

// PasteItem skips hashes and thumbnails that only synchronization needs.
func (s *Store) PasteItem(id string) (clip.Item, error) {
	return s.readItem(id, true)
}

func (s *Store) readItem(id string, forPaste bool) (clip.Item, error) {
	if err := s.errDisabled(); err != nil {
		return clip.Item{}, err
	}
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
	if forPaste {
		return clip.PNGForPaste(data)
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
	if err := s.errDisabled(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishLocked()
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
	if err := s.errDisabled(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishLocked()
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
		return s.removeFiles([]Entry{entry})
	}
	return os.ErrNotExist
}

// Clear drops every item that is not pinned.
func (s *Store) Clear() error {
	if err := s.errDisabled(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishLocked()
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
	return errors.Join(s.removeFiles(drop), s.cleanOrphansLocked())
}

func (s *Store) trimLocked() ([]Entry, error) {
	return s.trimAtLocked(time.Now())
}

func (s *Store) trimAtLocked(now time.Time) ([]Entry, error) {
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
	if s.capacity > 0 {
		var total int64
		for _, entry := range kept {
			size, err := s.entryBytesLocked(entry)
			if err != nil {
				return nil, err
			}
			total += size
		}
		removed := make(map[string]bool)
		for i := len(kept) - 1; i >= 0 && total > s.capacity; i-- {
			entry := kept[i]
			if !entry.Pinned {
				size, _ := s.entryBytesLocked(entry)
				total -= size
				removed[entry.ID] = true
				drop = append(drop, entry)
			}
		}
		if len(removed) > 0 {
			filtered := make([]Entry, 0, len(kept)-len(removed))
			for _, entry := range kept {
				if !removed[entry.ID] {
					filtered = append(filtered, entry)
				}
			}
			kept = filtered
		}
	}
	s.entries = kept
	return drop, nil
}

func (s *Store) removeFiles(entries []Entry) error {
	var failures []error
	remove := func(path string) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			failures = append(failures, err)
		}
	}
	for _, entry := range entries {
		delete(s.contentSizes, entry.ID)
		if entry.TextFile != "" {
			path, err := s.textPath(entry.TextFile)
			if err != nil {
				failures = append(failures, err)
			} else {
				remove(path)
			}
		}
		if entry.ImageFile != "" {
			remove(filepath.Join(s.dir, "blobs", entry.ImageFile))
			remove(filepath.Join(s.dir, "previews", entry.ID+".png"))
		}
		if entry.ThumbFile != "" {
			remove(filepath.Join(s.dir, "thumbs", entry.ThumbFile))
		}
	}
	return errors.Join(failures...)
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
