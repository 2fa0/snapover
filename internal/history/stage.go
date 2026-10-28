package history

import (
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/oklog/ulid/v2"
)

// stagedImageTTL is long enough for Preview or Photos to finish reading a
// file. Snapover stays running, so a session cannot keep every opened image.
const (
	stagedImageTTL            = 15 * time.Minute
	maxStagedImageBytes int64 = 128 << 20
)

type stagedImage struct {
	path string
	info os.FileInfo
}

var ErrOpenedCacheFull = errors.New("原图查看缓存已满，请等待 15 分钟后重试")

// StageImage copies a stored PNG into a private cache for the system viewer.
// The copy is not the history blob: saving from Preview must not rewrite the record.
// Unmodified copies are reused; recent files stay valid for the system viewer.
func (s *Store) StageImage(id string) (string, error) {
	if err := s.errDisabled(); err != nil {
		return "", err
	}
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
	now := time.Now()
	used, err := s.reapOpened(now)
	if err != nil {
		return "", err
	}
	if copy, ok := s.openedCopies[id]; ok {
		info, err := os.Lstat(copy.path)
		if err == nil && info.Mode().IsRegular() && os.SameFile(info, copy.info) && info.Size() == copy.info.Size() && info.ModTime().Equal(copy.info.ModTime()) {
			if err := os.Chtimes(copy.path, now, now); err != nil {
				return "", err
			}
			info, err = os.Stat(copy.path)
			if err != nil {
				return "", err
			}
			s.openedCopies[id] = stagedImage{copy.path, info}
			return copy.path, nil
		}
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if info.Size() > maxStagedImageBytes-used {
		return "", ErrOpenedCacheFull
	}
	dest := filepath.Join(dir, ulid.MustNew(ulid.Now(), rand.Reader).String()+".png")
	if err := copyRegularFile(src, dest, 0o600); err != nil {
		return "", err
	}
	info, err = os.Stat(dest)
	if err != nil {
		return "", err
	}
	if s.openedCopies == nil {
		s.openedCopies = make(map[string]stagedImage)
	}
	s.openedCopies[id] = stagedImage{dest, info}
	s.scheduleOpenedCleanup()
	return dest, nil
}

func (s *Store) scheduleOpenedCleanup() {
	if s.openedTimer == nil {
		s.openedTimer = time.AfterFunc(time.Minute, s.maintainOpened)
	}
}

func (s *Store) maintainOpened() {
	s.openedMu.Lock()
	defer s.openedMu.Unlock()
	s.openedTimer = nil
	if s.openedClosed {
		return
	}
	used, err := s.reapOpened(time.Now())
	if used > 0 || err != nil {
		s.scheduleOpenedCleanup()
	}
}

// CleanOpened waits for the current copy, stops further staging, and removes
// viewer files. A worker queued before quit must not recreate a file afterwards.
func (s *Store) CleanOpened() {
	s.openedMu.Lock()
	defer s.openedMu.Unlock()
	s.openedClosed = true
	if s.openedTimer != nil {
		s.openedTimer.Stop()
		s.openedTimer = nil
	}
	s.openedCopies = nil
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

func (s *Store) reapOpened(now time.Time) (int64, error) {
	entries, err := os.ReadDir(s.openedDir())
	if err != nil {
		return 0, err
	}
	var used int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return 0, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if now.Sub(info.ModTime()) >= stagedImageTTL {
			if err := os.Remove(filepath.Join(s.openedDir(), entry.Name())); err != nil && !os.IsNotExist(err) {
				return 0, err
			}
		} else {
			used += info.Size()
		}
	}
	for id, copy := range s.openedCopies {
		if _, err := os.Lstat(copy.path); os.IsNotExist(err) {
			delete(s.openedCopies, id)
		}
	}
	return used, nil
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
