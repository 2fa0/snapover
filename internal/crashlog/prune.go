package crashlog

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ordinaryName = "snapover.log"
	// retainFor bounds diagnostic files. Retention is per file: once the
	// earliest record is older than this, the whole file goes, even if the
	// tail is newer.
	retainFor = 7 * 24 * time.Hour
	// maxBackupBytes keeps a normal one-megabyte rotation, but not a log that
	// grew without bound because Windows rename cannot replace an existing
	// destination.
	maxBackupBytes = 2 << 20
	// maxInactiveBytes caps leftover logs other than the two active files,
	// the session marker, and one in-policy backup of each.
	maxInactiveBytes = 4 << 20
)

var timestampPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)

// Maintain drops diagnostic logs that are no longer useful. Active logs whose
// earliest record is older than 7 days are removed rather than kept as a
// backup. Inactive logs under logs/, and legacy log files sitting directly in
// dataDir, are removed when expired, oversized, or past the leftover cap.
// The open snapover.log, crash.log, and session.open are not deleted.
// Call it before those files are opened. Once they are open, use PruneInactive.
func Maintain(dataDir string) int {
	return maintain(dataDir, time.Now())
}

func maintain(dataDir string, now time.Time) int {
	logDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return 0
	}
	removed := 0
	for _, name := range []string{ordinaryName, fileName} {
		if retireOld(filepath.Join(logDir, name), now) {
			removed++
		}
	}
	return removed + pruneInactive(dataDir, now)
}

// PruneInactive removes expired and excess log files without renaming the
// active logs. It is safe while snapover.log and crash.log are open.
func PruneInactive(dataDir string) int {
	return pruneInactive(dataDir, time.Now())
}

func pruneInactive(dataDir string, now time.Time) int {
	return pruneDir(filepath.Join(dataDir, "logs"), now, true) + pruneDir(dataDir, now, false)
}

func retireOld(path string, now time.Time) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return false
	}
	started, ok := logStarted(path)
	if !ok || now.Sub(started) < retainFor {
		return false
	}
	if !renameAside(path, info.Size()) {
		return false
	}
	// The moved file is already past retention; do not leave it as .1.
	_ = os.Remove(path + ".1")
	return true
}

func renameAside(path string, size int64) bool {
	backup := path + ".1"
	// Windows refuses to rename onto an existing file. Remove the previous
	// backup first so a second rotation can succeed.
	_ = os.Remove(backup)
	if err := os.Rename(path, backup); err != nil {
		return false
	}
	if size > maxBackupBytes {
		_ = os.Remove(backup)
	}
	return true
}

func logStarted(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return time.Time{}, false
	}
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	if m := timestampPattern.Find(buf[:n]); m != nil {
		if t, err := time.Parse(time.RFC3339Nano, string(m)); err == nil {
			return t, true
		}
	}
	return info.ModTime(), true
}

type inactiveLog struct {
	path string
	mod  time.Time
	size int64
}

func pruneDir(dir string, now time.Time, inLogs bool) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var extra []inactiveLog
	var total int64
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !disposable(name, inLogs) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(dir, name)
		if extraGeneration(name) || info.Size() > maxBackupBytes || expired(path, info.ModTime(), now) {
			if os.Remove(path) == nil {
				removed++
			}
			continue
		}
		if protectedBackup(name) {
			continue
		}
		extra = append(extra, inactiveLog{path, info.ModTime(), info.Size()})
		total += info.Size()
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].mod.Before(extra[j].mod) })
	for _, item := range extra {
		if total <= maxInactiveBytes {
			break
		}
		if os.Remove(item.path) == nil {
			total -= item.size
			removed++
		}
	}
	return removed
}

func expired(path string, mod time.Time, now time.Time) bool {
	if !mod.IsZero() && now.Sub(mod) >= retainFor {
		return true
	}
	started, ok := logStarted(path)
	return ok && now.Sub(started) >= retainFor
}

func disposable(name string, inLogs bool) bool {
	lower := strings.ToLower(name)
	if inLogs && (lower == sessionName || lower == fileName || lower == ordinaryName) {
		return false
	}
	known := logPrefix(lower) && strings.Contains(lower, ".log")
	if !inLogs {
		return known
	}
	return known || strings.HasSuffix(lower, ".log") || strings.Contains(lower, ".log.")
}

func logPrefix(lower string) bool {
	return strings.HasPrefix(lower, "snapover") || strings.HasPrefix(lower, "crash") || strings.HasPrefix(lower, "clipbridge")
}

func extraGeneration(name string) bool {
	lower := strings.ToLower(name)
	i := strings.LastIndex(lower, ".log.")
	if i < 0 {
		return false
	}
	n, err := strconv.Atoi(lower[i+len(".log."):])
	return err == nil && n > 1
}

func protectedBackup(name string) bool {
	switch strings.ToLower(name) {
	case ordinaryName + ".1", fileName + ".1":
		return true
	default:
		return false
	}
}

// RotatingFile appends to the ordinary log and bounds it for a long-lived
// process. About 1 MiB is rotated to .1. A file whose earliest record is
// older than 7 days is replaced and that old content is not kept.
type RotatingFile struct {
	mu      sync.Mutex
	f       *os.File
	path    string
	size    int64
	started time.Time
}

// OpenOrdinary rotates path when due, then opens it for append.
func OpenOrdinary(path string) (*RotatingFile, error) {
	now := time.Now()
	if !retireOld(path, now) {
		_ = rotate(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	w := &RotatingFile{f: f, path: path, started: now}
	if info, err := f.Stat(); err == nil {
		w.size = info.Size()
	}
	if started, ok := logStarted(path); ok {
		w.started = started
	}
	return w, nil
}

func (w *RotatingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, os.ErrClosed
	}
	aged := w.expired()
	if w.size >= rotateAt || aged {
		w.rotate(aged)
		if w.f == nil {
			return 0, os.ErrClosed
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

func (w *RotatingFile) expired() bool {
	return !w.started.IsZero() && time.Since(w.started) >= retainFor
}

func (w *RotatingFile) rotate(aged bool) {
	if w.f != nil {
		_ = w.f.Sync()
		_ = w.f.Close()
		w.f = nil
	}
	moved := false
	if aged {
		moved = renameAside(w.path, 0)
		if moved {
			_ = os.Remove(w.path + ".1")
		}
	} else {
		moved = rotate(w.path)
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	w.f = f
	w.size = 0
	if info, err := f.Stat(); err == nil {
		w.size = info.Size()
	}
	if moved {
		w.started = time.Now()
	}
}
