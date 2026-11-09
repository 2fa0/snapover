package history

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/2fa0/snapover/internal/clip"
)

const backupRecordLimit = 30 << 20
const backupTotalLimit = 1 << 30
const backupEntryLimit = 100_000

type backupRecord struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	PNG  []byte `json:"png,omitempty"`
	Note string `json:"note,omitempty"`
}

// ExportPinned writes a single archive atomically; file references are not portable.
func (s *Store) ExportPinned(ctx context.Context, path string) (int, error) {
	return s.exportPinned(ctx, path, s.pinnedEntries())
}

func (s *Store) pinnedEntries() []Entry {
	var out []Entry
	for _, e := range s.List() {
		if e.Pinned && (e.Kind == string(clip.KindText) || e.Kind == string(clip.KindImage)) {
			out = append(out, e)
		}
	}
	return out
}

// ExportPinnedRange uses one-based inclusive positions in the newest-first list.
func (s *Store) ExportPinnedRange(ctx context.Context, path string, first, last int) (int, error) {
	entries := s.pinnedEntries()
	if first < 1 || last < first || last > len(entries) {
		return 0, errors.New("导出范围超出当前置顶记录")
	}
	return s.exportPinned(ctx, path, entries[first-1:last])
}

func (s *Store) exportPinned(ctx context.Context, path string, entries []Entry) (int, error) {
	if len(entries) > backupEntryLimit {
		return 0, errors.New("备份记录过多，请缩小导出范围")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".snapover-backup-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return 0, err
	}
	z := zip.NewWriter(f)
	header, err := z.Create("snapover-pins-v1")
	if err != nil {
		return 0, err
	}
	if _, err = header.Write([]byte("1\n")); err != nil {
		return 0, err
	}
	n, total := 0, 0
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		record, err := s.backupRecord(e)
		if err != nil {
			return n, err
		}
		raw, err := json.Marshal(record)
		if err != nil {
			return n, err
		}
		total += len(raw)
		if len(raw) > backupRecordLimit || total > backupTotalLimit {
			return n, errors.New("置顶备份超过 1 GiB 上限，请分批整理后再导出")
		}
		w, err := z.Create(fmt.Sprintf("%04d.json", n))
		if err != nil {
			return n, err
		}
		if _, err = w.Write(raw); err != nil {
			return n, err
		}
		n++
	}
	if err = z.Close(); err != nil {
		return n, err
	}
	if err = f.Sync(); err != nil {
		return n, err
	}
	if err = f.Close(); err != nil {
		return n, err
	}
	return n, os.Rename(f.Name(), path)
}

// Read encoded bodies directly: exporting an image never needs its pixels.
func (s *Store) backupRecord(e Entry) (backupRecord, error) {
	record := backupRecord{Kind: e.Kind, Note: e.Note, Text: e.Text}
	var path string
	var limit int64
	if e.Kind == string(clip.KindImage) {
		path = filepath.Join(s.dir, "blobs", e.ImageFile)
		limit = clip.MaxImageBytes
		record.Text = ""
	} else if e.TextFile != "" {
		var err error
		path, err = s.textPath(e.TextFile)
		if err != nil {
			return record, err
		}
		limit = clip.MaxTextBytes
	} else {
		return record, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return record, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return record, err
	}
	if int64(len(raw)) > limit {
		return record, errors.New("历史内容超过备份记录上限")
	}
	if e.Kind == string(clip.KindImage) {
		record.PNG = raw
	} else {
		record.Text = string(raw)
	}
	return record, nil
}

func readBackupRecord(f *zip.File, prepare bool) (backupRecord, clip.Item, error) {
	var record backupRecord
	if f.UncompressedSize64 > backupRecordLimit {
		return record, clip.Item{}, errors.New("备份记录超过大小上限")
	}
	r, err := f.Open()
	if err != nil {
		return record, clip.Item{}, err
	}
	defer r.Close()
	raw, err := io.ReadAll(io.LimitReader(r, backupRecordLimit+1))
	if err != nil {
		return record, clip.Item{}, err
	}
	if len(raw) > backupRecordLimit {
		return record, clip.Item{}, errors.New("备份记录超过大小上限")
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, clip.Item{}, err
	}
	var item clip.Item
	switch record.Kind {
	case string(clip.KindText):
		if len(record.PNG) != 0 {
			return record, item, errors.New("文本备份含无效图片字段")
		}
		item, err = clip.FromText(record.Text)
	case string(clip.KindImage):
		if len(record.PNG) > clip.MaxImageBytes {
			return record, item, errors.New("备份图片超过大小上限")
		}
		if prepare {
			item, err = clip.FromPNG(record.PNG)
		} else {
			// Fully validate the body, but don't hash or build previews that are discarded.
			item, err = clip.PNGForPaste(record.PNG)
		}
	default:
		err = errors.New("备份含不支持的记录类型")
	}
	item.ReleaseDecoded()
	return record, item, err
}

// ImportPinned validates the whole archive before appending. Disk failures may
// leave a completed prefix; the returned count always names that prefix.
func (s *Store) ImportPinned(ctx context.Context, path string) (int, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return 0, err
	}
	defer z.Close()
	if len(z.File) == 0 || len(z.File) > backupEntryLimit+1 || z.File[0].Name != "snapover-pins-v1" {
		return 0, errors.New("不是支持的 Snapover 置顶备份")
	}
	h, err := z.File[0].Open()
	if err != nil {
		return 0, err
	}
	header, err := io.ReadAll(io.LimitReader(h, 3))
	h.Close()
	if err != nil || string(header) != "1\n" {
		return 0, errors.New("不支持的置顶备份版本")
	}
	var total uint64
	for _, f := range z.File[1:] {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if f.UncompressedSize64 > backupRecordLimit {
			return 0, errors.New("备份记录超过大小上限")
		}
		total += f.UncompressedSize64
		if total > backupTotalLimit {
			return 0, errors.New("备份超过 1 GiB 上限")
		}
		if _, _, err := readBackupRecord(f, false); err != nil {
			return 0, err
		}
	}
	n := 0
	for _, f := range z.File[1:] {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		record, item, err := readBackupRecord(f, true)
		if err != nil {
			return n, err
		}
		e, err := s.add(DirLocal, "", item, true, record.Note)
		// A committed record may still report a failure deleting old assets.
		if e.ID != "" {
			n++
		}
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
