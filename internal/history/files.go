package history

import (
	"context"
	"errors"
	"io/fs"
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

// AddFiles records names and a one-level tree. File bodies stay out of
// history, so a copy does not consume history capacity.
func (s *Store) AddFiles(ctx context.Context, dir, peer string, item clip.Item, limit int64) (Entry, error) {
	if err := s.errDisabled(); err != nil {
		return Entry{}, err
	}
	if item.Kind != clip.KindFiles || item.Sensitive || item.Skip != "" {
		return Entry{}, errors.New("没有可记录的文件")
	}
	if limit < 0 {
		return Entry{}, errors.New("历史文件大小上限无效")
	}
	s.mu.Lock()
	epoch := s.fileEpoch
	s.mu.Unlock()
	at := time.Now()
	files, err := inspectClipboardFiles(ctx, item.Files)
	if err != nil {
		return Entry{}, err
	}
	entry := Entry{ID: ulid.Make().String(), At: at, Dir: dir, Kind: string(clip.KindFiles), Peer: peer, SourceApp: clip.CleanSource(item.SourceApp)}
	for _, f := range files {
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
	preview := filePreviewFrom(entry, files)
	entry.FileTree = preview.Items
	entry.FileMore = preview.More
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishLocked()
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	if epoch != s.fileEpoch {
		return Entry{}, context.Canceled
	}
	return s.addLocked(entry)
}

type fileMeta struct {
	Name      string
	Directory bool
	Size      int64
}

func inspectClipboardFiles(ctx context.Context, paths []string) ([]fileMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errors.New("没有可记录的文件")
	}
	var entries []fileMeta
	add := func(name string, dir bool, size int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(entries) >= filetransfer.MaxBundleEntries {
			return nil
		}
		if size < 0 {
			return errors.New("历史文件大小无效")
		}
		entries = append(entries, fileMeta{Name: name, Directory: dir, Size: size})
		return nil
	}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(p) || filepath.Clean(p) == filepath.VolumeName(p)+string(filepath.Separator) {
			return nil, errors.New("不能复制整个磁盘或相对路径")
		}
		info, err := os.Lstat(p)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(p)
		if info.IsDir() {
			if err := add(name, true, 0); err != nil {
				return nil, err
			}
			root, err := os.OpenRoot(p)
			if err != nil {
				return nil, err
			}
			err = fs.WalkDir(root.FS(), ".", func(local string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if local == "." {
					return nil
				}
				if len(entries) >= filetransfer.MaxBundleEntries {
					return fs.SkipAll
				}
				entry, err := d.Info()
				if err != nil {
					return err
				}
				rel := name + "/" + local
				if entry.IsDir() {
					return add(rel, true, 0)
				}
				if !entry.Mode().IsRegular() {
					return nil
				}
				return add(rel, false, entry.Size())
			})
			root.Close()
			if err != nil {
				return nil, err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := add(name, false, info.Size()); err != nil {
			return nil, err
		}
	}
	if len(entries) == 0 {
		return nil, errors.New("没有可记录的文件")
	}
	return entries, nil
}

func filePreviewFrom(entry Entry, files []fileMeta) FilePreview {
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
	return preview
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
	Name      string `json:"name,omitempty"`
	Directory bool   `json:"directory,omitempty"`
	Depth     int    `json:"depth,omitempty"`
}

func (s *Store) FilePreview(id string) (FilePreview, error) {
	if err := s.errDisabled(); err != nil {
		return FilePreview{}, err
	}
	s.mu.Lock()
	entry, ok := s.findLocked(id)
	s.mu.Unlock()
	if !ok || entry.Kind != string(clip.KindFiles) {
		return FilePreview{}, os.ErrNotExist
	}
	items := append([]FilePreviewItem(nil), entry.FileTree...)
	return FilePreview{
		Name:      fileTitle(entry),
		Directory: entry.Folder && entry.FileCount == 1,
		Items:     items,
		More:      entry.FileMore,
	}, nil
}

func (s *Store) dropFileBundles() error {
	changed := false
	for i, entry := range s.entries {
		if entry.Kind != string(clip.KindFiles) || entry.FileBundle == "" {
			continue
		}
		path, err := s.bundlePath(entry.FileBundle)
		if err == nil {
			if len(entry.FileTree) == 0 {
				if files, err := filetransfer.ReadBundle(path, int64(entry.Size)); err == nil {
					meta := make([]fileMeta, len(files))
					for i, f := range files {
						meta[i] = fileMeta{Name: f.Name, Directory: f.Directory, Size: f.Size}
					}
					preview := filePreviewFrom(entry, meta)
					entry.FileTree = preview.Items
					entry.FileMore = preview.More
				}
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		entry.FileBundle = ""
		s.entries[i] = entry
		changed = true
	}
	listing, err := os.ReadDir(filepath.Join(s.dir, "files"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, file := range listing {
		if file.IsDir() {
			continue
		}
		_ = os.Remove(filepath.Join(s.dir, "files", file.Name()))
	}
	if changed {
		return s.saveLocked()
	}
	return nil
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
