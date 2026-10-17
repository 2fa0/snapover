package history

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

const (
	checkpointOps    = 256
	checkpointBytes  = 1 << 20
	maxJournalRecord = 4 << 20
)

type historyChange struct {
	Sequence uint64   `json:"sequence"`
	Op       string   `json:"op"`
	Entry    *Entry   `json:"entry,omitempty"`
	ID       string   `json:"id,omitempty"`
	Pinned   bool     `json:"pinned,omitempty"`
	Drop     []string `json:"drop,omitempty"`
}

func entryIDs(entries []Entry) []string {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]string, len(entries))
	for i := range entries {
		ids[i] = entries[i].ID
	}
	return ids
}

func (s *Store) journalPath() string { return filepath.Join(s.dir, "history.log") }

// A successful mutation is written to the log before callers remove its blobs.
// Failed checkpoints leave the committed log intact and are retried later.
func (s *Store) persistLocked(change historyChange) error {
	change.Sequence = s.sequence + 1
	body, err := json.Marshal(change)
	if err != nil {
		return err
	}
	if len(body) > maxJournalRecord {
		return errors.New("历史变更记录过大")
	}
	header := make([]byte, 8)
	binary.LittleEndian.PutUint32(header, uint32(len(body)))
	binary.LittleEndian.PutUint32(header[4:], crc32.ChecksumIEEE(body))
	f, err := os.OpenFile(s.journalPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	start, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err = f.Write(header); err == nil {
		_, err = f.Write(body)
	}
	if err != nil {
		return errors.Join(err, f.Truncate(start))
	}
	s.sequence = change.Sequence
	s.journalOps++
	s.journalBytes = start + int64(len(header)+len(body))
	durable := false
	if s.journalOps >= checkpointOps || s.journalBytes >= checkpointBytes {
		// Snapshot first, then make the directory entry durable, and only then
		// truncate the journal. Replay skips sequences already in the snapshot.
		if s.saveLocked() == nil && dirDurable(s.dir) {
			durable = true
			if f.Truncate(0) == nil && syncFileData(f) == nil {
				s.journalOps, s.journalBytes = 0, 0
			}
		}
	}
	// File removal must not start until the drop can survive power loss.
	if !durable && len(change.Drop) > 0 {
		if err := syncFileData(f); err != nil || !dirDurable(s.dir) {
			if err == nil {
				err = errors.New("历史目录未能持久化")
			}
			s.rewindJournalLocked(f, start, change.Sequence)
			return err
		}
	}
	return nil
}

func (s *Store) rewindJournalLocked(f *os.File, start int64, sequence uint64) {
	s.sequence = sequence - 1
	if s.journalOps > 0 {
		s.journalOps--
	}
	s.journalBytes = start
	_ = f.Truncate(start)
	_ = syncFileData(f)
}

var syncFileData = func(f *os.File) error { return f.Sync() }

func dirDurable(path string) bool {
	err := syncDir(path)
	return err == nil || dirSyncUnsupported(err)
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	err = d.Sync()
	return errors.Join(err, d.Close())
}

func dirSyncUnsupported(err error) bool {
	if err == nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EINVAL)
}

func (s *Store) replayJournal() error {
	f, err := os.OpenFile(s.journalPath(), os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	var offset int64
	for {
		var header [8]byte
		n, err := io.ReadFull(f, header[:])
		if errors.Is(err, io.EOF) && n == 0 {
			break
		}
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return s.truncateJournalTail(f, offset)
		}
		if err != nil {
			return err
		}
		size := binary.LittleEndian.Uint32(header[:4])
		if size == 0 || size > maxJournalRecord {
			return fmt.Errorf("历史日志记录长度无效，位置 %d", offset)
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(f, body); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return s.truncateJournalTail(f, offset)
			}
			return err
		}
		if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(header[4:]) {
			return fmt.Errorf("历史日志校验失败，位置 %d", offset)
		}
		var change historyChange
		if err := json.Unmarshal(body, &change); err != nil {
			return err
		}
		if change.Sequence == 0 {
			return errors.New("历史日志缺少序号")
		}
		if change.Sequence > s.sequence {
			if change.Sequence != s.sequence+1 {
				return errors.New("历史日志序号不连续")
			}
			if err := s.applyChange(change); err != nil {
				return err
			}
			s.sequence = change.Sequence
		}
		offset += int64(len(header)) + int64(size)
		s.journalOps++
	}
	s.journalBytes = offset
	return nil
}

func (s *Store) truncateJournalTail(f *os.File, offset int64) error {
	if err := f.Truncate(offset); err != nil {
		return err
	}
	s.journalBytes = offset
	return f.Sync()
}

func (s *Store) applyChange(change historyChange) error {
	switch change.Op {
	case "add":
		if change.Entry == nil || change.Entry.ID == "" {
			return errors.New("历史日志中的新增记录无效")
		}
		s.entries = append([]Entry{*change.Entry}, s.entries...)
	case "pin":
		found := false
		for i := range s.entries {
			if s.entries[i].ID == change.ID {
				s.entries[i].Pinned = change.Pinned
				found = true
				break
			}
		}
		if !found {
			return errors.New("历史日志中的置顶记录不存在")
		}
	case "drop":
	default:
		return errors.New("历史日志包含未知操作")
	}
	if len(change.Drop) > 0 {
		drop := make(map[string]bool, len(change.Drop))
		for _, id := range change.Drop {
			drop[id] = true
		}
		kept := s.entries[:0]
		for _, entry := range s.entries {
			if !drop[entry.ID] {
				kept = append(kept, entry)
			}
		}
		clear(s.entries[len(kept):])
		s.entries = kept
	}
	return nil
}
