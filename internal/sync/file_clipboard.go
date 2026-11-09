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
		}
	}
}

func fileClipboardTitle(paths []string) string {
	if len(paths) == 0 {
		return "文件"
	}
	name := filepath.Base(paths[0])
	if name == "" || name == "." || name == "/" {
		name = "文件"
	}
	if len(paths) == 1 {
		return name
	}
	return fmt.Sprintf("%s 等 %d 项", name, len(paths))
}

func (s *Session) reportFileClipboard(item queuedItem, name, state string, done, total int64, message string) {
	if s.onFile != nil {
		s.onFile(Notice{
			Kind:          NoticeFile,
			FileClipboard: true,
			FileName:      name,
			FileState:     state,
			FileDone:      done,
			FileTotal:     total,
			Message:       message,
		})
	}
	if s.onClipboardResult == nil {
		return
	}
	ack := wire.Ack{ItemID: item.id, Message: message}
	switch state {
	case "complete":
		ack.State = "complete"
	case "error":
		ack.State = "error"
	case "cancelled":
		ack.State = "cancelled"
	default:
		ack.State = "sending"
	}
	s.onClipboardResult(ack)
}

func (s *Session) sendFileClipboard(item queuedItem) error {
	title := fileClipboardTitle(item.Files)
	report := func(state string, done, total int64, err error) {
		message := ""
		switch state {
		case "packing":
			message = "正在准备文件剪贴板…"
		case "sending":
			percent := int64(100)
			if total > 0 {
				percent = done * 100 / total
			}
			message = fmt.Sprintf("正在同步文件剪贴板… %d%%", percent)
		case "waiting":
			message = "正在同步文件剪贴板… 100%"
		case "error":
			if err != nil {
				message = err.Error()
			}
		}
		s.reportFileClipboard(item, title, state, done, total, message)
	}
	finish := func(err error) error {
		if err == nil {
			return nil
		}
		if errors.Is(err, errFileClipboardSuperseded) {
			s.reportFileClipboard(item, title, "cancelled", 0, 0, "")
			return err
		}
		report("error", 0, 0, err)
		return err
	}
	if !s.fileClipboardSupported {
		return finish(errors.New("对方版本不支持文件剪贴板，请更新对方的 Snapover"))
	}
	check := func() error {
		if s.fileClipboardCheck == nil {
			return errors.New("文件剪贴板未初始化")
		}
		return s.fileClipboardCheck(item)
	}
	if err := check(); err != nil {
		return finish(err)
	}
	report("packing", 0, 0, nil)
	if s.fileCache == nil {
		return finish(errors.New("文件剪贴板缓存未初始化"))
	}
	lease, err := s.fileCache.BeginSend()
	if err != nil {
		return finish(err)
	}
	defer lease.Release(false)
	archive, err := filetransfer.PackBundleIn(s.ctx, item.Files, s.maxFileBytes(), check, lease.Dir)
	if err != nil {
		return finish(err)
	}
	defer os.Remove(archive)
	var lastTotal int64
	_, err = s.sendFileMode(s.ctx, archive, func(done, total int64) {
		lastTotal = total
		state := "sending"
		if total > 0 && done >= total {
			state = "waiting"
		}
		report(state, done, total, nil)
	}, &item)
	if err != nil {
		return finish(err)
	}
	report("complete", lastTotal, lastTotal, nil)
	return nil
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
