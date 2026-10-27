package filetransfer

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	MaxBundleEntries       = 10000
	MaxBundlePath          = 1024
	bundleOverhead   int64 = 32 << 20
)

// BundleLimit includes bounded ZIP headers; payload bytes have a separate limit.
func BundleLimit(limit int64) int64 {
	const maxInt64 = int64(1<<63 - 1)
	if limit > maxInt64-bundleOverhead {
		return maxInt64
	}
	return limit + bundleOverhead
}

// bundleName rejects names that would escape or collide on either platform.
func bundleName(name string) error {
	if name == "" || len(strings.TrimSuffix(name, "/")) > MaxBundlePath || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return errors.New("文件路径无效或过长")
	}
	parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
	if len(parts) > 64 {
		return errors.New("文件夹层级超过 64 层")
	}
	for _, p := range parts {
		safe, err := SafeName(p)
		if err != nil || safe != p {
			return errors.New("文件名无法在 Windows 和 macOS 之间安全复制")
		}
	}
	return nil
}

type checkedReader struct {
	r     io.Reader
	check func() error
}

func (r checkedReader) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// PackBundle snapshots files without following links or buffering their bodies.
// check also cancels work when the originating clipboard generation changes.
func PackBundle(ctx context.Context, paths []string, limit int64, check func() error) (result string, err error) {
	return PackBundleIn(ctx, paths, limit, check, "")
}

// PackBundleIn stages on the destination volume for a later atomic rename.
func PackBundleIn(ctx context.Context, paths []string, limit int64, check func() error, tempDir string) (result string, err error) {
	if len(paths) == 0 || len(paths) > MaxBundleEntries {
		return "", errors.New("复制的文件数量超过上限或为空")
	}
	valid := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if check != nil {
			return check()
		}
		return nil
	}
	f, err := os.CreateTemp(tempDir, ".snapover-clipboard-*.zip")
	if err != nil {
		return "", err
	}
	result = f.Name()
	tempName := result
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(tempName)
		}
	}()
	w := zip.NewWriter(f)
	var total int64
	count := 0
	seen := make(map[string]bool)
	buffer := make([]byte, 256<<10)
	add := func(root *os.Root, local, name string, info fs.FileInfo) error {
		if err := valid(); err != nil {
			return err
		}
		if err := bundleName(name); err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("暂不支持复制符号链接或特殊文件")
		}
		key := strings.ToLower(name)
		if seen[key] {
			return errors.New("复制的文件存在跨平台重名，请分开复制")
		}
		seen[key] = true
		count++
		if count > MaxBundleEntries {
			return errors.New("文件和文件夹合计超过 10000 项")
		}
		if !info.IsDir() {
			if info.Size() < 0 || info.Size() > limit-total {
				return fmt.Errorf("复制内容合计超过 %d MB", limit>>20)
			}
			total += info.Size()
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = name
		h.Method = zip.Store
		if info.IsDir() {
			h.Name += "/"
		}
		dst, err := w.CreateHeader(h)
		if err != nil || info.IsDir() {
			return err
		}
		src, err := root.Open(local)
		if err != nil {
			return err
		}
		defer src.Close()
		before, err := src.Stat()
		if err != nil || !os.SameFile(info, before) || before.Size() != info.Size() || !before.ModTime().Equal(info.ModTime()) {
			return errors.New("源文件已改变，请重新复制")
		}
		n, err := io.CopyBuffer(dst, checkedReader{io.LimitReader(src, info.Size()+1), valid}, buffer)
		if err != nil {
			return err
		}
		after, err := src.Stat()
		if err != nil || n != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			return errors.New("源文件在复制期间发生变化，请重新复制")
		}
		return nil
	}
	for _, p := range paths {
		if err = valid(); err != nil {
			return result, err
		}
		if !filepath.IsAbs(p) || filepath.Clean(p) == filepath.VolumeName(p)+string(filepath.Separator) {
			return result, errors.New("不能复制整个磁盘或相对路径")
		}
		info, e := os.Lstat(p)
		if e != nil {
			return result, e
		}
		if info.IsDir() {
			root, e := os.OpenRoot(p)
			if e != nil {
				return result, e
			}
			opened, e := root.Stat(".")
			if e != nil || !os.SameFile(info, opened) {
				root.Close()
				return result, errors.New("源文件夹已改变，请重新复制")
			}
			err = fs.WalkDir(root.FS(), ".", func(local string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				entry, e := d.Info()
				if e != nil {
					return e
				}
				name := filepath.Base(p)
				if local != "." {
					name += "/" + local
				}
				return add(root, local, name, entry)
			})
			after, statErr := root.Stat(".")
			if err == nil && (statErr != nil || !after.ModTime().Equal(info.ModTime())) {
				err = errors.New("源文件夹在复制期间发生变化，请重新复制")
			}
			root.Close()
		} else {
			root, e := os.OpenRoot(filepath.Dir(p))
			if e != nil {
				return result, e
			}
			err = add(root, filepath.Base(p), filepath.Base(p), info)
			root.Close()
		}
		if err != nil {
			return result, err
		}
	}
	if err = w.Close(); err != nil {
		return result, err
	}
	if err = valid(); err != nil {
		return result, err
	}
	stat, err := f.Stat()
	if err != nil {
		return result, err
	}
	if stat.Size() > BundleLimit(limit) {
		return result, errors.New("文件目录信息超过上限")
	}
	return result, nil
}

// UnpackBundle publishes nothing until all paths, sizes and checksums pass.
// The destination is a fresh private directory, accessed through os.Root.
func UnpackBundle(ctx context.Context, archive, dir string, limit int64) ([]string, error) {
	f, z, entries, err := openBundle(archive, limit)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var tops []string
	topSeen := make(map[string]bool)
	for _, entry := range entries {
		top, _, _ := strings.Cut(entry.Name, "/")
		if !topSeen[top] {
			topSeen[top] = true
			tops = append(tops, filepath.Join(dir, top))
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var copied int64
	buffer := make([]byte, 256<<10)
	for _, f := range z.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := filepath.FromSlash(strings.TrimSuffix(f.Name, "/"))
		if f.FileInfo().IsDir() {
			if err := root.MkdirAll(name, 0700); err != nil {
				return nil, err
			}
			continue
		}
		if err := root.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return nil, err
		}
		dst, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600|f.Mode().Perm()&0111)
		if err != nil {
			return nil, err
		}
		src, err := f.Open()
		if err != nil {
			dst.Close()
			return nil, err
		}
		n, copyErr := io.CopyBuffer(dst, checkedReader{io.LimitReader(src, limit-copied+1), ctx.Err}, buffer)
		src.Close()
		closeErr := dst.Close()
		copied += n
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if copied > limit || uint64(n) != f.UncompressedSize64 {
			return nil, errors.New("文件解包大小不符")
		}
		// Timestamps are best effort; they do not change the received bytes.
		_ = root.Chtimes(name, f.Modified, f.Modified)
	}
	for _, p := range tops {
		if _, err := root.Stat(path.Base(filepath.ToSlash(p))); err != nil {
			return nil, err
		}
	}
	return tops, nil
}

// BundleEntry is bounded archive metadata; file bodies stay on disk.
type BundleEntry struct {
	Name      string
	Directory bool
	Size      int64
}

func ReadBundle(archive string, limit int64) ([]BundleEntry, error) {
	f, _, entries, err := openBundle(archive, limit)
	if f != nil {
		f.Close()
	}
	return entries, err
}

func openBundle(archive string, limit int64) (*os.File, *zip.Reader, []BundleEntry, error) {
	if limit < 0 {
		return nil, nil, nil, errors.New("文件大小上限无效")
	}
	f, err := os.Open(archive)
	if err != nil {
		return nil, nil, nil, err
	}
	fail := func(err error) (*os.File, *zip.Reader, []BundleEntry, error) { f.Close(); return nil, nil, nil, err }
	info, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if info.Size() > BundleLimit(limit) {
		return fail(errors.New("文件归档超过大小上限"))
	}
	if err := checkBundleDirectory(f, info.Size()); err != nil {
		return fail(err)
	}
	z, err := zip.NewReader(f, info.Size())
	if err != nil {
		return fail(err)
	}
	entries, err := bundleEntries(z, limit)
	if err != nil {
		return fail(err)
	}
	return f, z, entries, nil
}

func bundleEntries(z *zip.Reader, limit int64) ([]BundleEntry, error) {
	if len(z.File) == 0 || len(z.File) > MaxBundleEntries {
		return nil, errors.New("文件数量无效或超过 10000 项")
	}
	var total uint64
	seen := make(map[string]bool)
	directories := make(map[string]bool)
	spelling := make(map[string]string)
	entries := make([]BundleEntry, 0, len(z.File))
	for _, f := range z.File {
		if f.Method != zip.Store {
			return nil, errors.New("文件剪贴板只接受未压缩归档")
		}
		if err := bundleName(f.Name); err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(f.Name, "/")
		if parent := path.Dir(name); parent != "." && !directories[parent] {
			return nil, errors.New("归档缺少父文件夹或目录顺序无效")
		}
		if f.FileInfo().IsDir() {
			directories[name] = true
		}
		key := strings.ToLower(name)
		if seen[key] {
			return nil, errors.New("文件目录包含重名路径")
		}
		seen[key] = true
		if f.Mode()&os.ModeSymlink != 0 || !f.Mode().IsRegular() && !f.Mode().IsDir() {
			return nil, errors.New("不支持链接或特殊文件")
		}
		if f.UncompressedSize64 > uint64(limit)-total {
			return nil, errors.New("文件解包后超过大小上限")
		}
		total += f.UncompressedSize64
		parts := strings.Split(name, "/")
		for i := range parts {
			component := strings.Join(parts[:i+1], "/")
			folded := strings.ToLower(component)
			if old, ok := spelling[folded]; ok && old != component {
				return nil, errors.New("文件目录包含跨平台重名路径")
			}
			spelling[folded] = component
		}
		entries = append(entries, BundleEntry{Name: name, Directory: f.FileInfo().IsDir(), Size: int64(f.UncompressedSize64)})
	}
	return entries, nil
}
