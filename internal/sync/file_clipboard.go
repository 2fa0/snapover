package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/filetransfer"
	"github.com/2fa0/snapover/internal/wire"
)

var errFileClipboardSuperseded = errors.New("已有新的复制内容，已停止旧文件同步")

func filePathsSize(paths []string) int {
	n := 0
	for _, p := range paths {
		n += len(p) + 1
	}
	return n
}

func (e *Engine) clipboardFilePaths() ([]string, error) {
	before, err := e.Board.Sequence()
	if err != nil {
		return nil, err
	}
	if b, ok := e.Board.(interface{ FilePaths() ([]string, error) }); ok {
		paths, err := b.FilePaths()
		after, seqErr := e.Board.Sequence()
		if err == nil && (seqErr != nil || before != after) {
			return nil, clip.ErrClipboardBusy
		}
		return paths, err
	}
	item, err := e.Board.Snapshot()
	return item.Files, err
}

// MaintainClipboardCache protects the current native file-list snapshot,
// including a clipboard persisted by a previous application process.
func (e *Engine) MaintainClipboardCache(ctx context.Context) {
	if e.FileCache == nil {
		return
	}
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		paths, err := e.clipboardFilePaths()
		if err == nil {
			err = e.FileCache.Clean(paths)
		}
		if err != nil {
			e.Log.Warn("清理文件剪贴板缓存失败")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (e *Engine) checkFileClipboard(s *Session, item queuedItem) error {
	select {
	case <-s.done:
		return errors.New("连接已断开，文件未同步")
	default:
	}
	e.mu.Lock()
	current := e.sess == s
	e.mu.Unlock()
	if !current {
		return errors.New("连接已改变，文件未同步")
	}
	if ok, msg := e.admitClipboard(item); !ok {
		return errors.New(msg)
	}
	seq, err := e.Board.Sequence()
	if err != nil {
		return errors.New("无法读取剪贴板序号，文件未同步")
	}
	if seq != item.CopySequence {
		return errFileClipboardSuperseded
	}
	return nil
}

func (e *Engine) captureFileClipboard(s *Session, sequence, version uint64) (func(clip.Item) error, []string, error) {
	if err := e.rejectRemoteBeforePrepare(s, clip.Item{Kind: clip.KindFiles}); err != nil {
		return nil, nil, err
	}
	paths, err := e.clipboardFilePaths()
	if err != nil {
		return nil, nil, err
	}
	return func(item clip.Item) error {
		return e.deliverFromSequence(s, item, sequence, true, version)
	}, paths, nil
}

func (s *Session) queueFileClipboard(item queuedItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	// Only the newest waiting file copy is useful. An active copy checks its
	// originating native generation during packing and every network chunk.
	select {
	case <-s.fileClipboardJobs:
	default:
	}
	select {
	case s.fileClipboardJobs <- item:
	default:
	}
}

func (s *Session) fileClipboardLoop() {
	for {
		select {
		case <-s.done:
			return
		case item := <-s.fileClipboardJobs:
			err := s.sendFileClipboard(item)
			if errors.Is(err, errFileClipboardSuperseded) {
				continue
			}
			if s.fileClipboardCheck != nil && errors.Is(s.fileClipboardCheck(item), errFileClipboardSuperseded) {
				continue
			}
			if s.onClipboardResult != nil {
				result := wire.Ack{ItemID: item.id, State: "complete"}
				if err != nil {
					result.State = "error"
					result.Message = err.Error()
				}
				s.onClipboardResult(result)
			}
		}
	}
}

func (s *Session) sendFileClipboard(item queuedItem) error {
	if !s.fileClipboardSupported {
		return errors.New("对方版本不支持文件剪贴板，请更新对方的 Snapover")
	}
	check := func() error {
		if s.fileClipboardCheck == nil {
			return errors.New("文件剪贴板未初始化")
		}
		return s.fileClipboardCheck(item)
	}
	if err := check(); err != nil {
		return err
	}
	progress := func(message string) {
		if s.onClipboardResult != nil {
			s.onClipboardResult(wire.Ack{ItemID: item.id, State: "sending", Message: message})
		}
	}
	progress("正在准备文件剪贴板…")
	archive, err := filetransfer.PackBundle(s.ctx, item.Files, s.maxFileBytes(), check)
	if err != nil {
		return err
	}
	defer os.Remove(archive)
	_, err = s.sendFileMode(s.ctx, archive, func(done, total int64) {
		percent := int64(100)
		if total > 0 {
			percent = done * 100 / total
		}
		progress(fmt.Sprintf("正在同步文件剪贴板… %d%%", percent))
	}, &item)
	return err
}

type clipboardReceive struct {
	lease    *filetransfer.ClipboardLease
	publish  func(clip.Item) error
	source   string
	limit    int64
	prepared bool
}

func (s *Session) finishFileClipboard(current *fileReceive, archive string) error {
	defer os.Remove(archive)
	dir := filepath.Join(current.clipboard.lease.Dir, "items")
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	paths, err := filetransfer.UnpackBundle(s.ctx, archive, dir, current.clipboard.limit)
	if err != nil {
		return err
	}
	item, err := clip.FromFiles(paths)
	if err != nil {
		return err
	}
	item.SourceApp = clip.CleanSource(current.clipboard.source)
	// A failed native publication can still leave file URLs in the OS. Keep
	// completed files for cache maintenance to inspect instead of breaking them.
	current.clipboard.prepared = true
	return current.clipboard.publish(item)
}

func (f *fileReceive) abort() {
	f.file.Abort()
	if f.clipboard != nil {
		f.clipboard.lease.Release(false)
	}
}
