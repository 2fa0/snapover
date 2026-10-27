package history

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/filetransfer"
	"github.com/oklog/ulid/v2"
)

const MaxFilePreviewItems = 20

func (s *Store) bundlePath(name string) (string, error) {
	id, ok := strings.CutSuffix(name, ".zip")
	if !ok {
		return "", errors.New("历史文件归档名称无效")
	}
	if _, err := ulid.ParseStrict(id); err != nil {
		return "", errors.New("历史文件归档名称无效")
	}
	return filepath.Join(s.dir, "files", name), nil
}

// AddFiles snapshots content before taking the storage lock. Text writes and
// list readers can proceed while a large folder is being read. Clear revokes
// an unfinished snapshot; its late completion cannot repopulate cleared history.
func (s *Store) AddFiles(ctx context.Context, dir, peer string, item clip.Item, limit int64) (Entry, error) {
	if err := s.errDisabled(); err != nil {
		return Entry{}, err
	}
	if item.Kind != clip.KindFiles || item.Sensitive || item.Skip != "" {
		return Entry{}, errors.New("没有可记录的文件")
	}
	s.mu.Lock()
	epoch := s.fileEpoch
	capacity := s.capacity
	s.mu.Unlock()
	if capacity > 0 {
		limit = min(limit, capacity)
	}
	if limit < 0 {
		return Entry{}, errors.New("历史文件大小上限无效")
	}
	at := time.Now()
	archive, err := filetransfer.PackBundleIn(ctx, item.Files, limit, nil, filepath.Join(s.dir, "files"))
	if err != nil {
		return Entry{}, err
	}
	defer os.Remove(archive)
	entries, err := filetransfer.ReadBundle(archive, limit)
	if err != nil {
		return Entry{}, err
	}
	entry := Entry{ID: ulid.Make().String(), At: at, Dir: dir, Kind: string(clip.KindFiles), Peer: peer, SourceApp: clip.CleanSource(item.SourceApp)}
	for _, f := range entries {
		entry.Size += int(f.Size)
		if !strings.Contains(f.Name, "/") {
			if entry.FileCount == 0 {
				entry.FileName, entry.Folder = f.Name, f.Directory
			}
			entry.FileCount++
		}
	}
	if entry.FileCount == 0 {
		return Entry{}, errors.New("历史文件缺少顶层条目")
	}
	entry.FileBundle = entry.ID + ".zip"
	target, err := s.bundlePath(entry.FileBundle)
	if err != nil {
		return Entry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishLocked()
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	if epoch != s.fileEpoch {
		return Entry{}, context.Canceled
	}
	if err := os.Rename(archive, target); err != nil {
		return Entry{}, err
	}
	return s.addLocked(entry)
}

// FilePreview contains one folder's immediate children, or a bounded list of
// copied roots for a multi-selection. It never loads file bodies or recurses.
type FilePreview struct {
	Name      string
	Directory bool
	Items     []FilePreviewItem
	More      bool
}

type FilePreviewItem struct {
	Name      string
	Directory bool
	Depth     int
}

func (s *Store) FilePreview(id string) (FilePreview, error) {
	entry, archive, err := s.fileEntry(id)
	if err != nil {
		return FilePreview{}, err
	}
	files, err := filetransfer.ReadBundle(archive, int64(entry.Size))
	if err != nil {
		return FilePreview{}, err
	}
	preview := FilePreview{Name: fileTitle(entry), Directory: entry.Folder && entry.FileCount == 1}
	for _, f := range files {
		name := f.Name
		if entry.FileCount == 1 {
			prefix := entry.FileName + "/"
			if !preview.Directory || !strings.HasPrefix(name, prefix) {
				continue
			}
			name = strings.TrimPrefix(name, prefix)
		}
		depth := 0
		if entry.FileCount == 1 {
			if strings.Contains(name, "/") {
				continue
			}
		} else {
			depth = strings.Count(name, "/")
			if depth > 1 {
				continue
			}
			name = path.Base(name)
		}
		if len(preview.Items) == MaxFilePreviewItems {
			preview.More = true
			break
		}
		preview.Items = append(preview.Items, FilePreviewItem{Name: name, Directory: f.Directory, Depth: depth})
	}
	return preview, nil
}

func (s *Store) fileEntry(id string) (Entry, string, error) {
	if err := s.errDisabled(); err != nil {
		return Entry{}, "", err
	}
	s.mu.Lock()
	entry, ok := s.findLocked(id)
	s.mu.Unlock()
	if !ok || entry.Kind != string(clip.KindFiles) {
		return Entry{}, "", os.ErrNotExist
	}
	archive, err := s.bundlePath(entry.FileBundle)
	if entry.Size < 0 {
		return Entry{}, "", errors.New("历史文件大小无效")
	}
	return entry, archive, err
}

// PrepareFiles extracts a fresh paste copy into the shared clipboard cache.
// Deleting history or changing the pasted copy cannot damage the other copy.
// The caller releases the lease, keeping it only after native publication.
func (s *Store) PrepareFiles(ctx context.Context, id string, cache *filetransfer.ClipboardCache, protected []string) (clip.Item, *filetransfer.ClipboardLease, error) {
	if cache == nil {
		return clip.Item{}, nil, errors.New("文件剪贴板缓存未初始化")
	}
	entry, archive, err := s.fileEntry(id)
	if err != nil {
		return clip.Item{}, nil, err
	}
	// Unpacking is cancelable and happens off the UI thread.
	info, err := os.Stat(archive)
	if err != nil {
		return clip.Item{}, nil, err
	}
	lease, err := cache.Begin(protected, int64(entry.Size), info.Size())
	if err != nil {
		return clip.Item{}, nil, err
	}
	paths, err := filetransfer.UnpackBundle(ctx, archive, lease.Dir, int64(entry.Size))
	if err != nil {
		lease.Release(false)
		return clip.Item{}, nil, err
	}
	item, err := clip.FromFiles(paths)
	if err != nil {
		lease.Release(false)
		return clip.Item{}, nil, err
	}
	item.SourceApp = entry.SourceApp
	return item, lease, nil
}

func fileTitle(entry Entry) string {
	if entry.FileCount > 1 {
		return entry.FileName + " 等 " + strconv.Itoa(entry.FileCount) + " 项"
	}
	if entry.FileName != "" {
		return entry.FileName
	}
	return "文件"
}
