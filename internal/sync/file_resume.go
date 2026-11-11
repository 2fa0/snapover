package sync

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/2fa0/snapover/internal/wire"
	"github.com/oklog/ulid/v2"
)

const resumeWait = 15 * time.Minute

// ErrFileOutcomeUnknown means cancellation could not be confirmed by the receiver.
var ErrFileOutcomeUnknown = errors.New("传输已停止，但无法确认对方是否已保存，请检查对方接收文件夹")

type fileControlKey struct{}

// FileControl belongs to one outgoing file. It never changes the destination.
type FileControl struct {
	id     string
	paused atomic.Bool
}

func NewFileControl() *FileControl       { return &FileControl{id: ulid.Make().String()} }
func (c *FileControl) Pause(paused bool) { c.paused.Store(paused) }
func (c *FileControl) Paused() bool      { return c.paused.Load() }

func (s *Session) isClosed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *Session) fileHeartbeat(ctx context.Context, results <-chan wire.FileResult, id string) error {
	if err := s.write(wire.TypeFileChunk, wire.FileChunk{ItemID: id}); err != nil {
		s.Close()
		return err
	}
	_, err := s.waitFileResult(ctx, results, "chunk")
	return err
}

// SendFileControlled retries only a lost connection to the same authenticated device.
func (e *Engine) SendFileControlled(ctx context.Context, peerID, path string, control *FileControl, progress func(int64, int64), waiting func()) (string, error) {
	original, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	ctx = context.WithValue(ctx, fileControlKey{}, control)
	deadline := time.Now().Add(resumeWait)
	waitingForResume := false
	for {
		if err := ctx.Err(); err != nil {
			if waitingForResume {
				return "", ErrFileOutcomeUnknown
			}
			return "", err
		}
		e.mu.Lock()
		s := e.sess
		e.mu.Unlock()
		if s != nil && !s.isClosed() {
			if s.peerID != peerID {
				if waitingForResume {
					return "", ErrFileOutcomeUnknown
				}
				return "", errors.New("在线设备已改变，已停止发送")
			}
			if !s.fileResumeSupported {
				if waitingForResume {
					return "", ErrFileOutcomeUnknown
				}
				return "", errors.New("暂停和续传需要双方更新 Snapover")
			}
			info, err := os.Stat(path)
			if err != nil || info.Size() != original.Size() || !info.ModTime().Equal(original.ModTime()) {
				if waitingForResume {
					return "", ErrFileOutcomeUnknown
				}
				return "", errors.New("源文件已改变，请重新发送")
			}
			name, err := s.sendFile(ctx, path, progress)
			if err == nil {
				return name, nil
			}
			if ctx.Err() != nil && s.isClosed() && !errors.Is(err, ctx.Err()) {
				return "", ErrFileOutcomeUnknown
			}
			if ctx.Err() != nil || !s.isClosed() {
				return name, err
			}
			waitingForResume = true
			deadline = time.Now().Add(resumeWait)
		}
		if time.Now().After(deadline) {
			if waitingForResume {
				return "", ErrFileOutcomeUnknown
			}
			return "", errors.New("等待原设备重连超时，请重新发送")
		}
		if waiting != nil {
			waiting()
		}
		select {
		case <-ctx.Done():
			if waitingForResume {
				return "", ErrFileOutcomeUnknown
			}
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

type resumeEntry struct {
	file  *fileReceive
	saved string
	timer *time.Timer
}

// At most eight partial/completed transfers are retained, for fifteen minutes.
// Keys include the authenticated certificate fingerprint, never just a filename.
type resumeFiles struct {
	mu      sync.Mutex
	entries map[string]*resumeEntry
	closed  bool
	busy    map[string]chan struct{}
}

func (r *resumeFiles) put(peer string, f *fileReceive, saved string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		if saved == "" {
			f.abort()
		}
		return
	}
	if r.entries == nil {
		r.entries = make(map[string]*resumeEntry)
	}
	key := peer + ":" + f.id
	if old := r.entries[key]; old != nil {
		old.timer.Stop()
		if old.file != f && old.saved == "" {
			old.file.abort()
		}
	}
	if len(r.entries) >= 8 {
		for k, old := range r.entries {
			old.timer.Stop()
			if old.saved == "" {
				old.file.abort()
			}
			delete(r.entries, k)
			break
		}
	}
	entry := &resumeEntry{file: f, saved: saved}
	r.entries[key] = entry
	entry.timer = time.AfterFunc(resumeWait, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.entries[key] == entry {
			delete(r.entries, key)
			if saved == "" {
				f.abort()
			}
		}
	})
}
func (r *resumeFiles) take(peer, id string) (*fileReceive, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := peer + ":" + id
	entry := r.entries[key]
	if entry == nil {
		return nil, ""
	}
	entry.timer.Stop()
	delete(r.entries, key)
	return entry.file, entry.saved
}

func (r *resumeFiles) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for k, entry := range r.entries {
		entry.timer.Stop()
		if entry.saved == "" {
			entry.file.abort()
		}
		delete(r.entries, k)
	}
}

// A replacement session must wait for the old disk worker to release ownership.
func (r *resumeFiles) acquire(peer, id string, done <-chan struct{}) bool {
	key := peer + ":" + id
	for {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return false
		}
		if r.busy == nil {
			r.busy = make(map[string]chan struct{})
		}
		wait := r.busy[key]
		if wait == nil {
			r.busy[key] = make(chan struct{})
			r.mu.Unlock()
			return true
		}
		r.mu.Unlock()
		select {
		case <-done:
			return false
		case <-wait:
		}
	}
}
func (r *resumeFiles) release(peer, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := peer + ":" + id
	if wait := r.busy[key]; wait != nil {
		delete(r.busy, key)
		close(wait)
	}
}
