package filetransfer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const clipboardCacheGrace = 15 * time.Minute

// ClipboardCache keeps native file URLs valid across app restarts. The
// clipboard snapshot and live transfers are protected; old pastes get a grace
// period. Admission bounds disk use instead of deleting a paste in progress.
type ClipboardCache struct {
	mu   sync.Mutex
	dir  string
	live map[string]bool
}

func NewClipboardCache(dir string) *ClipboardCache {
	return &ClipboardCache{dir: dir, live: make(map[string]bool)}
}

type ClipboardLease struct {
	Dir   string
	cache *ClipboardCache
}

func (l *ClipboardLease) Release(keep bool) {
	l.cache.mu.Lock()
	defer l.cache.mu.Unlock()
	delete(l.cache.live, filepath.Base(l.Dir))
	if !keep {
		_ = os.RemoveAll(l.Dir)
	}
}

func (c *ClipboardCache) cleanLocked(protected []string) (int, int64, error) {
	if c.dir == "" || !filepath.IsAbs(c.dir) {
		return 0, 0, errors.New("文件剪贴板缓存目录未设置")
	}
	if err := os.MkdirAll(c.dir, 0700); err != nil {
		return 0, 0, err
	}
	canonical, err := filepath.EvalSymlinks(c.dir)
	if err != nil {
		return 0, 0, err
	}
	c.dir = canonical
	pinned := make(map[string]bool)
	for _, p := range protected {
		rel, err := filepath.Rel(c.dir, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if resolved, resolveErr := filepath.EvalSymlinks(p); resolveErr == nil {
				rel, err = filepath.Rel(c.dir, resolved)
			}
		}
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			pinned[strings.Split(rel, string(filepath.Separator))[0]] = true
		}
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return 0, 0, err
	}
	count := 0
	var bytes int64
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "copy-") || !e.IsDir() {
			continue
		}
		p := filepath.Join(c.dir, e.Name())
		info, err := e.Info()
		if err != nil {
			return 0, 0, err
		}
		if !pinned[e.Name()] && !c.live[e.Name()] && time.Since(info.ModTime()) > clipboardCacheGrace {
			if err := os.RemoveAll(p); err != nil {
				return 0, 0, err
			}
			continue
		}
		count++
		err = filepath.WalkDir(p, func(_ string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type().IsRegular() {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if info.Size() > int64(1<<63-1)-bytes {
					return errors.New("文件缓存大小溢出")
				}
				bytes += info.Size()
			}
			return nil
		})
		if err != nil {
			return 0, 0, err
		}
	}
	return count, bytes, nil
}

func (c *ClipboardCache) Clean(protected []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _, err := c.cleanLocked(protected)
	return err
}

func (c *ClipboardCache) Begin(protected []string, limit, archiveBytes int64) (*ClipboardLease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	count, bytes, err := c.cleanLocked(protected)
	if err != nil {
		return nil, err
	}
	// Reserve archive plus extracted bytes for every live receive, including
	// one that has not yet reached disk. Saturate user-configurable limits.
	unit := BundleLimit(limit)
	const max = int64(1<<63 - 1)
	budget := max
	if unit <= max/3 {
		budget = 3 * unit
	}
	if budget < 1<<30 {
		budget = 1 << 30
	}
	if archiveBytes < 0 || archiveBytes > unit {
		return nil, errors.New("文件剪贴板大小无效或超过上限")
	}
	reserve := max
	if archiveBytes <= max-min(archiveBytes, limit) {
		reserve = archiveBytes + min(archiveBytes, limit)
	}
	if count >= 128 || bytes > budget-reserve || len(c.live) > 0 {
		return nil, errors.New("文件剪贴板缓存繁忙或已满，请稍后重新复制")
	}
	dir, err := os.MkdirTemp(c.dir, "copy-")
	if err != nil {
		return nil, err
	}
	c.live[filepath.Base(dir)] = true
	return &ClipboardLease{Dir: dir, cache: c}, nil
}
