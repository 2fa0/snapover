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
	// Match the previous index writer's per-operation durability. Sync when
	// checkpointing instead of making each small UI action wait on the disk.
	if err != nil {
		return errors.Join(err, f.Truncate(start))
	}
	s.sequence = change.Sequence
	s.journalOps++
	s.journalBytes = start + int64(len(header)+len(body))
	if s.journalOps >= checkpointOps || s.journalBytes >= checkpointBytes {
		// The sequence in the snapshot makes replay safe if the process exits
		// after replacing the snapshot but before truncating the old journal.
		if s.saveLocked() == nil && f.Truncate(0) == nil {
			if f.Sync() == nil {
				s.journalOps, s.journalBytes = 0, 0
			}
		}
	}
	return nil
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
