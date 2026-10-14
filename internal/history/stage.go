package history

import (
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/oklog/ulid/v2"
)

// stagedImageTTL is long enough for Preview or Photos to finish reading a
// file. Snapover stays running, so a session cannot keep every opened image.
const stagedImageTTL = 15 * time.Minute

// StageImage copies a stored PNG into a private cache for the system viewer.
// The copy is not the history blob: saving from Preview must not rewrite the record.
// The file stays until the next launch or quit, because the viewer may still be reading it.
func (s *Store) StageImage(id string) (string, error) {
	s.openedMu.Lock()
	defer s.openedMu.Unlock()
	if s.openedClosed {
		return "", os.ErrClosed
	}
	s.mu.Lock()
	entry, ok := s.findLocked(id)
	s.mu.Unlock()
	if !ok || entry.Kind != string(clip.KindImage) || entry.ImageFile == "" {
		return "", os.ErrNotExist
	}
	src := filepath.Join(s.dir, "blobs", entry.ImageFile)
	dir := s.openedDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, ulid.MustNew(ulid.Now(), rand.Reader).String()+".png")
	if err := copyRegularFile(src, dest, 0o600); err != nil {
		return "", err
	}
	s.reapOpened(time.Now())
	return dest, nil
}

// CleanOpened waits for the current copy, stops further staging, and removes
// viewer files. A worker queued before quit must not recreate a file afterwards.
func (s *Store) CleanOpened() {
	s.openedMu.Lock()
	defer s.openedMu.Unlock()
	s.openedClosed = true
	s.cleanOpened()
}

func (s *Store) openedDir() string { return filepath.Join(s.dir, "opened") }

func (s *Store) cleanOpened() {
	entries, err := os.ReadDir(s.openedDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		_ = os.Remove(filepath.Join(s.openedDir(), entry.Name()))
	}
}

func (s *Store) reapOpened(now time.Time) {
	entries, err := os.ReadDir(s.openedDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < stagedImageTTL {
			continue
		}
		_ = os.Remove(filepath.Join(s.openedDir(), entry.Name()))
	}
}

func copyRegularFile(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return os.ErrInvalid
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if cerr != nil {
		_ = os.Remove(dest)
		return cerr
	}
	return nil
}
