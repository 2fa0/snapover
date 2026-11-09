package history

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	ID         string            `json:"id"`
	At         time.Time         `json:"at"`
	Dir        string            `json:"dir"`
	Kind       string            `json:"kind"`
	Text       string            `json:"text,omitempty"`
	TextFile   string            `json:"text_file,omitempty"`
	Width      int               `json:"width,omitempty"`
	Height     int               `json:"height,omitempty"`
	Size       int               `json:"size,omitempty"`
	Peer       string            `json:"peer,omitempty"`
	ImageFile  string            `json:"image,omitempty"`
	ThumbFile  string            `json:"thumb,omitempty"`
	Pinned     bool              `json:"pinned,omitempty"`
	PinKey     int               `json:"pin_key,omitempty"`
	Note       string            `json:"note,omitempty"`
	SourceApp  string            `json:"source_app,omitempty"`
	FileBundle string            `json:"file_bundle,omitempty"`
	FilePaths  []string          `json:"file_paths,omitempty"`
	PinFiles   []string          `json:"pin_files,omitempty"`
	FileName   string            `json:"file_name,omitempty"`
	FileCount  int               `json:"file_count,omitempty"`
	Folder     bool              `json:"folder,omitempty"`
	FileTree   []FilePreviewItem `json:"file_tree,omitempty"`
	FileMore   bool              `json:"file_more,omitempty"`
	// Search matches are display-only; bodies and persisted records stay intact.
	MatchText   string       `json:"-"`
	MatchField  string       `json:"-"`
	MatchRanges []MatchRange `json:"-"`
}

// MatchRange is one match of a query inside MatchText, as byte offsets of the
// snippet MatchText is. Every match in the snippet carries one, so a row can
// show what matched throughout the text, not only the first one.
type MatchRange struct {
	Start int
	End   int
}

// Store keeps the newest clipboard items on disk.
type Store struct {
	mu           sync.Mutex
	listMu       sync.RWMutex
	listed       []Entry
	openedMu     sync.Mutex
	openedClosed bool
	openedCopies map[string]stagedImage
	openedTimer  *time.Timer
	dir          string
	entries      []Entry
	sequence     uint64
	journalOps   int
	journalBytes int64
	capacity     int64
	contentSizes map[string]int64
	fileEpoch    uint64
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
	// Staging files are never indexed. Leave recent ones alone because the
	// diagnostic -check process can open history while the app is still running.
	texts, err := os.ReadDir(filepath.Join(dir, "texts"))
	if err != nil {
		return nil, err
	}
	for _, file := range texts {
		if suffix, ok := strings.CutPrefix(file.Name(), ".text-"); ok && suffix != "" && strings.Trim(suffix, "0123456789") == "" && file.Type().IsRegular() {
			info, err := file.Info()
			if err != nil {
				return nil, err
			}
			if time.Since(info.ModTime()) < 24*time.Hour {
				continue
			}
			if err := os.Remove(filepath.Join(dir, "texts", file.Name())); err != nil {
				return nil, err
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "opened"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0700); err != nil {
		return nil, err
	}
	staging, err := os.ReadDir(filepath.Join(dir, "files"))
	if err != nil {
		return nil, err
	}
	for _, file := range staging {
		if strings.HasPrefix(file.Name(), ".snapover-clipboard-") && !file.IsDir() {
			_ = os.Remove(filepath.Join(dir, "files", file.Name()))
		}
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
	if err := s.dropFileBundles(); err != nil {
		return nil, err
	}
	// Old snapshots have no pin keys. Journal the assignments using the
	// existing pin operation, which older versions can still replay.
	for _, entry := range s.entries {
		if entry.Pinned && entry.PinKey == 0 && (entry.Kind == string(clip.KindText) || entry.Kind == string(clip.KindImage)) && s.freePinKey() != 0 {
			if err := s.SetPinned(entry.ID, true); err != nil {
				return nil, err
			}
		}
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

// Add stores a clipboard snapshot. dir is DirLocal or DirRemote.
func (s *Store) Add(dir, peer string, item clip.Item) (Entry, error) {
	return s.add(dir, peer, item, false, "")
}

func (s *Store) add(dir, peer string, item clip.Item, pinned bool, note string) (Entry, error) {
	if err := s.errDisabled(); err != nil {
		return Entry{}, err
	}
	if item.Kind == clip.KindFiles {
		return s.AddFiles(context.Background(), dir, peer, item)
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
	entry.Pinned, entry.Note = pinned, CleanNote(note)
	if pinned {
		entry.PinKey = s.freePinKey()
	}
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
	return s.addLocked(entry)
}

// addLocked commits an entry only after its external content is ready.
func (s *Store) addLocked(entry Entry) (Entry, error) {
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
	s.insertEntryLocked(entry)
	drop, err := s.trimLocked()
	if err != nil {
		s.entries = before
		return Entry{}, errors.Join(err, s.removeFiles([]Entry{entry}))
	}
	if _, kept := s.findLocked(entry.ID); !kept {
		s.entries = before
		return Entry{}, errors.Join(errors.New("历史容量不足，未保存本条内容"), s.removeFiles([]Entry{entry}))
	}
	if err := s.persistLocked(historyChange{Op: "add", Entry: &entry, Drop: entryIDs(drop)}); err != nil {
		s.entries = before
		return Entry{}, errors.Join(err, s.removeFiles([]Entry{entry}))
	}
	return entry, s.removeFiles(drop)
}

func (s *Store) insertEntryLocked(entry Entry) {
	before := s.entries
	index := 0
	for index < len(s.entries) && s.entries[index].At.After(entry.At) {
		index++
	}
	s.entries = make([]Entry, 0, len(before)+1)
	s.entries = append(s.entries, before[:index]...)
	s.entries = append(s.entries, entry)
	s.entries = append(s.entries, before[index:]...)
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
	if entry.Kind == string(clip.KindFiles) {
		if paths := ActiveFilePaths(entry); len(paths) > 0 {
			if item, err := clip.FromFiles(paths); err == nil {
				return item, nil
			}
		}
		return clip.Item{}, ErrFilesUnavailable
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

func (s *Store) otherPinnedBytesLocked(excludeID string) (int64, error) {
	var total int64
	for _, existing := range s.entries {
		if existing.Pinned && existing.ID != excludeID {
			size, err := s.entryBytesLocked(existing)
			if err != nil {
				return 0, err
			}
			total += size
		}
	}
	return total, nil
}

// SetPinned keeps an item out of age and count cleanup.
func (s *Store) SetPinned(id string, on bool) error {
	if err := s.errDisabled(); err != nil {
		return err
	}
	var snapshot []string
	if on {
		s.mu.Lock()
		entry, ok := s.findLocked(id)
		var budget int64 = DefaultMaxBytes
		var budgetErr error
		if ok && s.capacity > 0 {
			pinnedBytes, err := s.otherPinnedBytesLocked(id)
			if err != nil {
				budgetErr = err
			} else if rem := s.capacity - pinnedBytes; rem <= 0 {
				budgetErr = errors.New("历史容量不足，未保存本条内容，请提高上限或清理置顶记录")
			} else if rem < budget {
				budget = rem
			}
		}
		s.mu.Unlock()
		if !ok {
			return os.ErrNotExist
		}
		if budgetErr != nil {
			return budgetErr
		}
		if entry.Kind == string(clip.KindFiles) && !HasFileSnapshot(entry) {
			copied, err := s.snapshotPinnedFiles(id, ActiveFilePaths(entry), budget)
			if err != nil {
				return err
			}
			snapshot = copied
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishLocked()
	for i := range s.entries {
		if s.entries[i].ID != id {
			continue
		}
		beforeEntries := append([]Entry(nil), s.entries...)
		hadSnapshot := HasFileSnapshot(s.entries[i])
		s.entries[i].Pinned = on
		if !on {
			s.entries[i].PinKey = 0
			s.entries[i].Note = ""
			s.entries[i].PinFiles = nil
			delete(s.contentSizes, id)
		} else if s.entries[i].PinKey == 0 && (s.entries[i].Kind == string(clip.KindText) || s.entries[i].Kind == string(clip.KindImage)) {
			s.entries[i].PinKey = s.freePinKey()
		}
		if len(snapshot) > 0 {
			s.entries[i].PinFiles = snapshot
			delete(s.contentSizes, id)
		}
		var drop []Entry
		if on && s.capacity > 0 {
			size, err := s.entryBytesLocked(s.entries[i])
			if err == nil {
				var other int64
				other, err = s.otherPinnedBytesLocked(id)
				if err == nil && size+other > s.capacity {
					err = errors.New("历史容量不足，未保存本条内容，请提高上限或清理置顶记录")
				}
			}
			if err == nil {
				drop, err = s.trimLocked()
			}
			if err != nil {
				s.entries = beforeEntries
				delete(s.contentSizes, id)
				if len(snapshot) > 0 {
					_ = os.RemoveAll(s.pinFilesDir(id))
				}
				return err
			}
		}
		updated, ok := s.findLocked(id)
		if !ok {
			s.entries = beforeEntries
			if len(snapshot) > 0 {
				_ = os.RemoveAll(s.pinFilesDir(id))
			}
			return os.ErrNotExist
		}
		change := historyChange{
			Op:       "pin",
			ID:       id,
			Pinned:   on,
			PinKey:   updated.PinKey,
			Note:     updated.Note,
			PinFiles: updated.PinFiles,
			Drop:     entryIDs(drop),
		}
		if err := s.persistLocked(change); err != nil {
			s.entries = beforeEntries
			delete(s.contentSizes, id)
			if len(snapshot) > 0 {
				_ = os.RemoveAll(s.pinFilesDir(id))
			}
			return err
		}
		if !on && hadSnapshot {
			_ = os.RemoveAll(s.pinFilesDir(id))
		}
		if len(drop) > 0 {
			return s.removeFiles(drop)
		}
		return nil
	}
	if len(snapshot) > 0 {
		_ = os.RemoveAll(s.pinFilesDir(id))
	}
	return os.ErrNotExist
}

func (s *Store) SetNote(id, note string) error {
	if err := s.errDisabled(); err != nil {
		return err
	}
	note = CleanNote(note)
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishLocked()
	for i := range s.entries {
		if s.entries[i].ID == id {
			if !s.entries[i].Pinned {
				return errors.New("请先置顶记录后再设置备注")
			}
			before := s.entries[i].Note
			if before == note {
				return nil
			}
			s.entries[i].Note = note
			if err := s.persistLocked(historyChange{
				Op:     "pin",
				ID:     id,
				Pinned: s.entries[i].Pinned,
				PinKey: s.entries[i].PinKey,
				Note:   note,
			}); err != nil {
				s.entries[i].Note = before
				return err
			}
			return nil
		}
	}
	return os.ErrNotExist
}

// freePinKey is called while mu is held, or before the store is published.
func (s *Store) freePinKey() int {
	var used [10]bool
	for _, entry := range s.entries {
		if entry.Pinned && entry.PinKey > 0 && entry.PinKey < len(used) {
			used[entry.PinKey] = true
		}
	}
	for key := 1; key < len(used); key++ {
		if !used[key] {
			return key
		}
	}
	return 0
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
	s.fileEpoch++
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
		if HasFileSnapshot(entry) {
			if err := os.RemoveAll(s.pinFilesDir(entry.ID)); err != nil && !os.IsNotExist(err) {
				failures = append(failures, err)
			}
		}
		if entry.FileBundle != "" {
			path, err := s.bundlePath(entry.FileBundle)
			if err != nil {
				failures = append(failures, err)
			} else {
				remove(path)
			}
		}
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
