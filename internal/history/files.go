package history

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/filetransfer"
	"github.com/oklog/ulid/v2"
)

const MaxFilePreviewItems = 20

var ErrFilesUnavailable = errors.New("文件内容未保存在历史中，请重新复制后再粘贴")

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
func (s *Store) AddFiles(ctx context.Context, dir, peer string, item clip.Item) (Entry, error) {
	if err := s.errDisabled(); err != nil {
		return Entry{}, err
	}
	if item.Kind != clip.KindFiles || item.Sensitive || item.Skip != "" {
		return Entry{}, errors.New("没有可记录的文件")
	}
	s.mu.Lock()
	epoch := s.fileEpoch
	s.mu.Unlock()
	at := time.Now()
	files, err := inspectClipboardFiles(ctx, item.Files)
	if err != nil {
		return Entry{}, err
	}
	paths := make([]string, len(item.Files))
	for i, p := range item.Files {
		paths[i] = filepath.Clean(p)
	}
	entry := Entry{ID: ulid.Make().String(), At: at, Dir: dir, Kind: string(clip.KindFiles), Peer: peer, SourceApp: clip.CleanSource(item.SourceApp), FilePaths: paths}
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

// Inspect roots independently of the preview budget. Directory bodies and
// descendants are never scanned merely to record a clipboard event.
func inspectClipboardFiles(ctx context.Context, paths []string) ([]fileMeta, error) {
	if len(paths) == 0 {
		return nil, errors.New("没有可记录的文件")
	}
	var entries []fileMeta
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
		if !info.IsDir() && !info.Mode().IsRegular() {
			continue
		}
		size := info.Size()
		if info.IsDir() {
			size = 0
		}
		entries = append(entries, fileMeta{Name: filepath.Base(p), Directory: info.IsDir(), Size: size})
	}
	if len(entries) == 0 {
		return nil, errors.New("没有可记录的文件")
	}
	// Multi-selections preview the selected roots. Only a single folder needs
	// a bounded read of its immediate children, plus one overflow indicator.
	if len(entries) == 1 && entries[0].Directory && len(paths) == 1 {
		f, err := os.Open(paths[0])
		if err != nil {
			return nil, err
		}
		defer f.Close()
		children, err := f.ReadDir(MaxFilePreviewItems + 1)
		if err != nil && err != io.EOF {
			return nil, err
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			entries = append(entries, fileMeta{Name: entries[0].Name + "/" + child.Name(), Directory: child.IsDir()})
		}
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

func pathsAvailable(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return false
		}
		info, err := os.Lstat(p)
		if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
			return false
		}
	}
	return true
}

// ActiveFilePaths returns the live paths to use for pasting or revealing:
// original paths first, falling back to the pinned snapshot when the original
// files were moved or deleted.
func ActiveFilePaths(entry Entry) []string {
	if entry.Kind != string(clip.KindFiles) {
		return nil
	}
	if pathsAvailable(entry.FilePaths) {
		return entry.FilePaths
	}
	if pathsAvailable(entry.PinFiles) {
		return entry.PinFiles
	}
	return nil
}

// RevealFilePath returns the primary existing path to show in Finder or Explorer.
func RevealFilePath(entry Entry) (string, bool) {
	paths := ActiveFilePaths(entry)
	if len(paths) == 0 {
		return "", false
	}
	return paths[0], true
}

// FilesAvailable reports whether every recorded top-level path still exists on
// disk (either the original path, an uncleaned clipboard-files cache, or a
// pinned file snapshot).
func FilesAvailable(entry Entry) bool {
	return len(ActiveFilePaths(entry)) > 0
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

func (s *Store) pinFilesDir(id string) string {
	return filepath.Join(s.dir, "pinned-files", id)
}

// HasFileSnapshot reports whether the entry has a saved pin copy.
func HasFileSnapshot(entry Entry) bool {
	if len(entry.PinFiles) > 0 {
		return true
	}
	needle := string(filepath.Separator) + "pinned-files" + string(filepath.Separator) + entry.ID + string(filepath.Separator)
	for _, p := range entry.FilePaths {
		if strings.Contains(p, needle) {
			return true
		}
	}
	return false
}

const maxPinDirectoryDepth = 64

type pinCopyBudget struct {
	remaining int64
	entries   int
}

func (s *Store) snapshotPinnedFiles(id string, paths []string, maxBytes int64) ([]string, error) {
	if !pathsAvailable(paths) {
		return nil, ErrFilesUnavailable
	}
	if len(paths) > filetransfer.MaxBundleEntries {
		return nil, errors.New("文件和文件夹合计超过 10000 项")
	}
	if maxBytes <= 0 {
		return nil, errors.New("历史容量不足，未保存本条内容，请提高上限或清理置顶记录")
	}
	dest := s.pinFilesDir(id)
	if err := os.RemoveAll(dest); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return nil, err
	}
	budget := &pinCopyBudget{remaining: maxBytes}
	out := make([]string, 0, len(paths))
	used := map[string]bool{}
	for _, p := range paths {
		base := filepath.Base(p)
		name := base
		for i := 2; used[strings.ToLower(name)]; i++ {
			name = strings.TrimSuffix(base, filepath.Ext(base)) + "-" + strconv.Itoa(i) + filepath.Ext(base)
		}
		used[strings.ToLower(name)] = true
		target := filepath.Join(dest, name)
		if err := copyFileTree(p, target, 1, budget); err != nil {
			_ = os.RemoveAll(dest)
			return nil, err
		}
		out = append(out, target)
	}
	return out, nil
}

func copyFileTree(src, dst string, depth int, budget *pinCopyBudget) error {
	if depth > maxPinDirectoryDepth {
		return errors.New("文件夹层级超过 64 层")
	}
	budget.entries++
	if budget.entries > filetransfer.MaxBundleEntries {
		return errors.New("文件和文件夹合计超过 10000 项")
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return errors.New("暂不支持复制符号链接或特殊文件")
	}
	if info.IsDir() {
		return copyDirTree(src, dst, depth, budget)
	}
	if info.Size() < 0 || info.Size() > budget.remaining {
		return errors.New("历史容量不足，未保存本条内容，请提高上限或清理置顶记录")
	}
	budget.remaining -= info.Size()
	return copyRegularFile(src, dst, 0o600)
}

func copyDirTree(src, dst string, depth int, budget *pinCopyBudget) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		from := filepath.Join(src, entry.Name())
		to := filepath.Join(dst, entry.Name())
		if err := copyFileTree(from, to, depth+1, budget); err != nil {
			return err
		}
	}
	return nil
}
