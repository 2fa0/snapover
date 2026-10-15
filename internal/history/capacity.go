package history

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/oklog/ulid/v2"
)

const DefaultMaxBytes int64 = 2 << 30

type Usage struct {
	ContentBytes int64
	PinnedBytes  int64
	TotalBytes   int64
}

// SetCapacity also prunes oldest unpinned entries using the same durable log
// as Add. Pins count toward usage but are never automatically deleted.
func (s *Store) SetCapacity(maxBytes int64) error {
	if maxBytes <= 0 {
		return errors.New("历史容量上限必须大于零")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.entries
	beforeCapacity := s.capacity
	s.capacity = maxBytes
	s.contentSizes = nil // Recheck disk assets when applying or retrying maintenance.
	drop, err := s.trimLocked()
	if err != nil {
		s.capacity = beforeCapacity
		return err
	}
	if len(drop) == 0 {
		return s.cleanOrphansLocked()
	}
	if err := s.persistLocked(historyChange{Op: "drop", Drop: entryIDs(drop)}); err != nil {
		s.entries = before
		s.capacity = beforeCapacity
		return err
	}
	return errors.Join(s.removeFiles(drop), s.cleanOrphansLocked())
}

func (s *Store) entryBytesLocked(entry Entry) (int64, error) {
	if size, ok := s.contentSizes[entry.ID]; ok {
		return size, nil
	}
	size, err := s.entryBytes(entry)
	if err != nil {
		return 0, err
	}
	if s.contentSizes == nil {
		s.contentSizes = make(map[string]int64)
	}
	s.contentSizes[entry.ID] = size
	return size, nil
}

func (s *Store) entryBytes(entry Entry) (int64, error) {
	size := int64(len(entry.Text))
	paths := []string{}
	if entry.TextFile != "" {
		path, err := s.textPath(entry.TextFile)
		if err != nil {
			return 0, err
		}
		paths = append(paths, path)
	}
	if entry.ImageFile != "" {
		paths = append(paths, filepath.Join(s.dir, "blobs", entry.ImageFile), filepath.Join(s.dir, "previews", entry.ID+".png"))
	}
	if entry.ThumbFile != "" {
		paths = append(paths, filepath.Join(s.dir, "thumbs", entry.ThumbFile))
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if !info.Mode().IsRegular() {
			return 0, fmt.Errorf("历史文件不是普通文件：%s", path)
		}
		size += info.Size()
	}
	return size, nil
}

// Retry leftovers from interrupted or failed deletes. Only generated asset names
// absent from the durable index are eligible; opened viewer copies stay intact.
// Add writes under the same lock, so its unpublished assets cannot be collected.
func (s *Store) cleanOrphansLocked() error {
	live := make(map[string]bool, len(s.entries))
	for _, entry := range s.entries {
		live[entry.ID] = true
	}
	var failures []error
	for _, dir := range []string{"blobs", "thumbs", "previews", "texts"} {
		entries, err := os.ReadDir(filepath.Join(s.dir, dir))
		if err != nil {
			failures = append(failures, err)
			continue
		}
		suffix := ".png"
		if dir == "texts" {
			suffix = ".txt"
		}
		for _, entry := range entries {
			id := strings.TrimSuffix(entry.Name(), suffix)
			if id == entry.Name() || live[id] {
				continue
			}
			if _, err := ulid.ParseStrict(id); err != nil {
				continue
			}
			if err := os.Remove(filepath.Join(s.dir, dir, entry.Name())); err != nil && !os.IsNotExist(err) {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

// Usage scans only when requested by settings, never on the clipboard path.
// TotalBytes also includes the index, journal and temporary viewer copies.
func (s *Store) Usage() (Usage, error) {
	s.mu.Lock()
	entries := append([]Entry(nil), s.entries...)
	s.contentSizes = nil
	s.mu.Unlock()
	var usage Usage
	for _, entry := range entries {
		size, err := s.entryBytes(entry)
		if err != nil {
			return Usage{}, err
		}
		usage.ContentBytes += size
		if entry.Pinned {
			usage.PinnedBytes += size
		}
	}
	err := filepath.WalkDir(s.dir, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			usage.TotalBytes += info.Size()
		}
		return nil
	})
	return usage, err
}
