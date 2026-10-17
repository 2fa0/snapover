package history

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// Unavailable keeps the damaged directory untouched and rejects writes.
// Callers must not treat a failed open as an empty history.
func Unavailable(dir string, reason error) *Store {
	if reason == nil {
		reason = errors.New("历史无法打开")
	}
	return &Store{
		dir:          dir,
		disabled:     reason,
		capacity:     DefaultMaxBytes,
		contentSizes: map[string]int64{},
	}
}

// Disabled is non-nil when this store cannot read or write history.
func (s *Store) Disabled() error {
	if s == nil {
		return errors.New("历史不可用")
	}
	return s.disabled
}

func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

func (s *Store) errDisabled() error {
	if err := s.Disabled(); err != nil {
		return fmt.Errorf("历史不可用：%w", err)
	}
	return nil
}

// Quarantine moves a damaged history directory aside. It does not delete it.
func Quarantine(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("没有历史目录")
	}
	dest := dir + ".damaged-" + time.Now().Format("20060102-150405")
	if _, err := os.Stat(dest); err == nil {
		dest = dir + ".damaged-" + time.Now().Format("20060102-150405.000")
	}
	if err := os.Rename(dir, dest); err != nil {
		return "", err
	}
	return dest, nil
}
