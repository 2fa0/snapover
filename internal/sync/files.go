package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/filetransfer"
	"github.com/2fa0/snapover/internal/settings"
	"github.com/2fa0/snapover/internal/wire"
	"github.com/oklog/ulid/v2"
	"github.com/zeebo/blake3"
)

const fileWait = 60 * time.Second

// Keep the backlog bounded independently of the configurable file limit.
// Preserve the existing backlog budget when the clipboard default changes.
const (
	fileQueueSize  = (200<<20)/wire.ChunkSize + 4
	fileQueueBytes = (200 << 20) + fileQueueSize*1024
	fileSendWindow = 8
)

type fileFrame struct {
	typ               byte
	payload           []byte
	sequence, version uint64
	sequenceErr       error
}

func (s *Session) queueFileFrame(typ byte, payload []byte) bool {
	frame := fileFrame{typ: typ, payload: payload, version: s.clipboardVersion.Load()}
	if typ == wire.TypeFileCancel {
		var cancel wire.ImageEnd
		if wire.Unmarshal(payload, &cancel) == nil {
			s.fileCancelID.Store(&cancel.ItemID)
		}
	}
	if typ == wire.TypeFileBegin {
		var begin wire.FileBegin
		if wire.Unmarshal(payload, &begin) == nil && begin.Clipboard {
			frame.version = s.clipboardVersion.Add(1)
			if s.clipboardSequence != nil {
				frame.sequence, frame.sequenceErr = s.clipboardSequence()
			} else {
				frame.sequenceErr = errors.New("文件剪贴板未初始化")
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(payload)
	if s.closed || n > wire.ChunkSize+1024 || n > fileQueueBytes-s.fileFrameBytes {
		return false
	}
	select {
	case s.fileFrames <- frame:
		s.fileFrameBytes += n
		return true
	default:
		return false
	}
}

func (s *Session) fileLoop() {
	var incoming *fileReceive
	defer func() {
		if incoming != nil {
			cancelID := s.fileCancelID.Load()
			cancelled := cancelID != nil && *cancelID == incoming.id
			if incoming.resumable && s.resumeFiles != nil && !cancelled {
				s.fileNotice(incoming, "waiting", "", "连接已断开，等待重连续传")
				s.resumeFiles.put(s.resumePeer, incoming, "")
				s.resumeFiles.release(s.resumePeer, incoming.id)
			} else {
				incoming.abort()
				if incoming.resumable {
					s.resumeFiles.release(s.resumePeer, incoming.id)
				}
				s.fileNotice(incoming, "error", "", "连接已断开，未完成的文件已清理，请重新发送")
			}
		}
	}()
	timer := time.NewTimer(fileWait)
	timer.Stop()
	defer timer.Stop()
	var timeout <-chan time.Time
	for {
		select {
		case <-s.done:
			return
		case <-timeout:
			s.rejectFile(incoming.id, "接收超时，请重新发送")
			incoming.abort()
			s.fileNotice(incoming, "error", "", "接收超时，请重新发送")
			if incoming.resumable {
				s.resumeFiles.release(s.resumePeer, incoming.id)
			}
			incoming, timeout = nil, nil
		case frame := <-s.fileFrames:
			// Close discards queued work, even if another disk call just returned.
			select {
			case <-s.done:
				return
			default:
			}
			previous := incoming
			incoming = s.receiveFileFrame(frame, incoming)
			if previous != nil && previous.resumable && previous != incoming {
				s.resumeFiles.release(s.resumePeer, previous.id)
			}
			s.mu.Lock()
			s.fileFrameBytes = max(0, s.fileFrameBytes-len(frame.payload))
			s.mu.Unlock()
			timer.Stop()
			timeout = nil
			if incoming != nil {
				wait := fileWait
				if incoming.resumable {
					wait = resumeWait
				}
				timer.Reset(time.Until(incoming.updated.Add(wait)))
				timeout = timer.C
			}
		}
	}
}

func (e *Engine) SetFileLimit(maxBytes int64) {
	e.mu.Lock()
	e.maxFile = maxBytes
	e.mu.Unlock()
}

func (e *Engine) FileLimit() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.maxFile <= 0 {
		return settings.DefaultMaxFileMB << 20
	}
	return e.maxFile
}

func (s *Session) maxFileBytes() int64 {
	if s.fileLimit != nil {
		return s.fileLimit()
	}
	return settings.DefaultMaxFileMB << 20
}

func (e *Engine) SetReceiveDir(dir string) {
	e.mu.Lock()
	e.receiveDir = dir
	e.mu.Unlock()
}

func (e *Engine) ReceiveDir() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.receiveDir
}

// SendFile is an explicit action, independent of clipboard pause/text policy.
// It returns only after the receiver confirms the file is saved.
func (e *Engine) SendFile(ctx context.Context, peerID, path string, progress func(int64, int64)) (string, error) {
	e.mu.Lock()
	s := e.sess
	e.mu.Unlock()
	if s == nil {
		return "", errors.New("没有在线设备，请先连接设备")
	}
	if s.peerID != peerID {
		return "", errors.New("在线设备已改变，请确认设备后重新发送")
	}
	if !s.fileSupported {
		return "", errors.New("对方版本不支持发送文件，请更新两边的 Snapover")
	}
	return s.sendFile(ctx, path, progress)
}

// SendFiles sends paths in order, one acknowledged transfer per file. Keeping
// each file a separate transfer means peers that only know single-file sends
// receive the whole batch, and a failure never publishes a half-received batch.
// It stops at the first failure and reports the names already saved.
func (e *Engine) SendFiles(ctx context.Context, peerID string, paths []string, progress func(index int, done, total int64)) ([]string, error) {
	sizes, err := filetransfer.ValidatePaths(paths, math.MaxInt64)
	if err != nil {
		return nil, err
	}
	var total int64
	for _, size := range sizes {
		total += size
	}
	saved := make([]string, 0, len(paths))
	var offset int64
	for i, path := range paths {
		if err := ctx.Err(); err != nil {
			return saved, err
		}
		index, base := i, offset
		name, err := e.SendFile(ctx, peerID, path, func(done, _ int64) {
			if progress != nil {
				progress(index, base+done, total)
			}
		})
		if err != nil {
			if len(paths) == 1 {
				return saved, err
			}
			return saved, fmt.Errorf("%s：%w", filepath.Base(path), err)
		}
		saved = append(saved, name)
		offset += sizes[i]
	}
	return saved, nil
}

func (s *Session) sendFile(ctx context.Context, path string, progress func(int64, int64)) (string, error) {
	if s.fileResumeSupported {
		if ctx.Value(fileControlKey{}) == nil {
			ctx = context.WithValue(ctx, fileControlKey{}, NewFileControl())
		}
	}
	return s.sendFileMode(ctx, path, progress, nil)
}

func (s *Session) sendFileMode(ctx context.Context, path string, progress func(int64, int64), clipboard *queuedItem) (saved string, err error) {
	if !s.fileSendMu.TryLock() {
		return "", errors.New("已有文件正在发送，请等待完成")
	}
	defer s.fileSendMu.Unlock()
	limit := int64(math.MaxInt64)
	if clipboard != nil {
		limit = s.maxFileBytes()
		limit = filetransfer.BundleLimit(limit)
	}
	if _, err := filetransfer.Validate(path, limit); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("打开文件失败：%w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return "", fmt.Errorf("请选择 %d MB 以内的普通文件", limit>>20)
	}
	name, err := filetransfer.SafeName(filepath.Base(path))
	if err != nil {
		return "", err
	}
	id := ulid.Make().String()
	control, _ := ctx.Value(fileControlKey{}).(*FileControl)
	resumable := clipboard == nil && control != nil && s.fileResumeSupported
	if resumable {
		id = control.id
	}
	begin := wire.FileBegin{ItemID: id, Name: name, TotalSize: info.Size(), Resumable: resumable}
	if clipboard != nil {
		if err = s.fileClipboardCheck(*clipboard); err != nil {
			return "", err
		}
		begin.Clipboard = true
		begin.Name = fileClipboardTitle(clipboard.Files)
		begin.SourceApp = clip.CleanSource(clipboard.SourceApp)
	}
	rejected := false
	results := make(chan wire.FileResult, fileSendWindow+4)
	s.fileResultMu.Lock()
	s.fileSendID, s.fileResults = id, results
	s.fileResultMu.Unlock()
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			if !resumable {
				s.Close()
				return
			}
			// Force a blocked write to finish; normal cancellation keeps the session usable.
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				s.Close()
			case <-watchDone:
			}
		case <-watchDone:
		}
	}()
	defer func() {
		if err != nil && !rejected {
			cancelConfirmed := false
			if resumable && !s.isClosed() {
				timer := time.AfterFunc(2*time.Second, s.Close)
				_ = s.write(wire.TypeFileCancel, wire.ImageEnd{ItemID: id})
			cancelWait:
				for {
					select {
					case result := <-results:
						switch result.State {
						case "complete":
							saved, err = result.Message, nil
							cancelConfirmed = true
							break cancelWait
						case "error":
							err = fmt.Errorf("接收失败：%s", result.Message)
							cancelConfirmed = true
							break cancelWait
						case "cancelled":
							cancelConfirmed = true
							break cancelWait
						case "unknown":
							break cancelWait
						}
					case <-s.done:
						break cancelWait
					}
				}
				timer.Stop()
			} else if !resumable {
				if ctx.Err() != nil {
					s.Close()
				} else {
					_ = s.write(wire.TypeFileCancel, wire.ImageEnd{ItemID: id})
				}
			}
			if resumable && ctx.Err() != nil && !cancelConfirmed {
				err = ErrFileOutcomeUnknown
			}
		}
		s.fileResultMu.Lock()
		s.fileSendID, s.fileResults = "", nil
		s.fileResultMu.Unlock()
	}()
	if err = s.write(wire.TypeFileBegin, begin); err != nil {
		s.Close()
		return "", err
	}
	ready, readyErr := s.waitFileResult(ctx, results, "ready")
	err = readyErr
	rejected = ready.State == "error"
	if ready.State == "complete" {
		return ready.Message, nil
	}
	if err != nil {
		return "", err
	}
	if ready.Offset < 0 || ready.Offset > info.Size() {
		return "", errors.New("续传位置无效")
	}
	if progress != nil {
		progress(ready.Offset, info.Size())
	}
	hash := blake3.New()
	buf := make([]byte, wire.ChunkSize)
	var offset int64
	last := time.Now()
	heartbeat := time.Now()
	acknowledged := ready.Offset
	waitChunk := func() error {
		ack, ackErr := s.waitFileResult(ctx, results, "chunk")
		if ackErr != nil {
			return ackErr
		}
		if ack.Offset != acknowledged+min(int64(wire.ChunkSize), info.Size()-acknowledged) || ack.Offset > offset {
			return errors.New("接收端确认的文件位置无效")
		}
		acknowledged = ack.Offset
		if progress != nil && (time.Since(last) >= 100*time.Millisecond || acknowledged == info.Size()) {
			progress(acknowledged, info.Size())
			last = time.Now()
		}
		return nil
	}
	for offset < info.Size() {
		if resumable {
			for control.Paused() {
				for acknowledged < offset {
					if err = waitChunk(); err != nil {
						return "", err
					}
				}
				if err = s.fileHeartbeat(ctx, results, id); err != nil {
					return "", err
				}
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(time.Second):
				}
			}
		}
		if clipboard != nil {
			if err = s.fileClipboardCheck(*clipboard); err != nil {
				return "", err
			}
		}
		if err = ctx.Err(); err != nil {
			return "", err
		}
		if resumable && offset-acknowledged >= fileSendWindow*wire.ChunkSize {
			if err = waitChunk(); err != nil {
				return "", err
			}
		} else if !resumable {
			select {
			case result := <-results:
				return "", fmt.Errorf("接收失败：%s", result.Message)
			default:
			}
		}
		n := int64(len(buf))
		if offset < ready.Offset {
			n = min(n, ready.Offset-offset)
		}
		if n > info.Size()-offset {
			n = info.Size() - offset
		}
		if _, err = io.ReadFull(f, buf[:n]); err != nil {
			return "", fmt.Errorf("读取源文件失败，文件可能已变化或磁盘发生 I/O 错误：%w", err)
		}
		_, _ = hash.Write(buf[:n])
		if offset >= ready.Offset {
			if err = s.write(wire.TypeFileChunk, wire.FileChunk{ItemID: id, Offset: offset, Data: buf[:n]}); err != nil {
				s.Close()
				return "", err
			}
		} else if resumable && time.Since(heartbeat) >= time.Second {
			if err = s.fileHeartbeat(ctx, results, id); err != nil {
				return "", err
			}
			heartbeat = time.Now()
		}
		offset += n
		if !resumable && progress != nil && (time.Since(last) >= 100*time.Millisecond || offset == info.Size()) {
			progress(offset, info.Size())
			last = time.Now()
		}
	}
	for resumable && acknowledged < offset {
		if err = waitChunk(); err != nil {
			return "", err
		}
	}
	after, statErr := f.Stat()
	if statErr != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return "", errors.New("源文件在发送期间发生变化，请重新发送")
	}
	if clipboard != nil {
		if err = s.fileClipboardCheck(*clipboard); err != nil {
			return "", err
		}
	}
	if err = s.write(wire.TypeFileEnd, wire.FileEnd{ItemID: id, Hash: hash.Sum(nil)}); err != nil {
		s.Close()
		return "", err
	}
	result, err := s.waitFileResult(ctx, results, "complete")
	if err != nil {
		return "", err
	}
	return result.Message, nil
}

func (s *Session) waitFileResult(ctx context.Context, results <-chan wire.FileResult, state string) (wire.FileResult, error) {
	timer := time.NewTimer(fileWait)
	if control, _ := ctx.Value(fileControlKey{}).(*FileControl); control != nil {
		timer.Reset(resumeWait)
	}
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return wire.FileResult{}, ctx.Err()
	case <-s.done:
		return wire.FileResult{}, errors.New("连接已断开，请重连后重新发送")
	case <-timer.C:
		return wire.FileResult{}, errors.New("等待对方确认超时，请检查对方接收文件夹后重试")
	case r := <-results:
		if r.State != state {
			return r, fmt.Errorf("接收失败：%s", r.Message)
		}
		return r, nil
	}
}

type fileReceive struct {
	id, name          string
	file              *filetransfer.Incoming
	updated, notified time.Time
	clipboard         *clipboardReceive
	resumable         bool
}

func (s *Session) fileNotice(f *fileReceive, state, path, message string) {
	if s.onFile == nil || f == nil || f.file == nil {
		return
	}
	s.onFile(Notice{Kind: NoticeFile, FileName: f.name, FileState: state, FilePath: path, FileDone: f.file.Written, FileTotal: f.file.Size, Message: message, FileClipboard: f.clipboard != nil})
}

func (s *Session) rejectFile(id, message string) {
	s.respond(wire.TypeFileResult, wire.FileResult{ItemID: id, State: "error", Message: message})
}

func (s *Session) receiveFile(typ byte, payload []byte, current *fileReceive) *fileReceive {
	return s.receiveFileFrame(fileFrame{typ: typ, payload: payload, version: s.clipboardVersion.Load()}, current)
}

func (s *Session) receiveFileFrame(frame fileFrame, current *fileReceive) (received *fileReceive) {
	typ, payload := frame.typ, frame.payload
	if typ == wire.TypeFileBegin {
		var b wire.FileBegin
		if wire.Unmarshal(payload, &b) != nil || b.ItemID == "" || len(b.ItemID) > 64 {
			return current
		}
		if current != nil {
			s.rejectFile(b.ItemID, "正在接收另一个文件，请稍后重试")
			return current
		}
		if b.Resumable && (b.Clipboard || !s.fileResumeSupported || s.resumeFiles == nil) {
			s.rejectFile(b.ItemID, "对方版本不支持断点续传")
			return nil
		}
		if b.Resumable {
			if !s.resumeFiles.acquire(s.resumePeer, b.ItemID, s.done) {
				return nil
			}
			defer func() {
				if received == nil {
					s.resumeFiles.release(s.resumePeer, b.ItemID)
				}
			}()
			if previous, saved := s.resumeFiles.take(s.resumePeer, b.ItemID); previous != nil {
				if previous.name != b.Name || previous.file.Size != b.TotalSize {
					previous.abort()
					s.rejectFile(b.ItemID, "续传文件已改变")
					return nil
				}
				if saved != "" {
					s.resumeFiles.put(s.resumePeer, previous, saved)
					s.respond(wire.TypeFileResult, wire.FileResult{ItemID: b.ItemID, State: "complete", Message: saved})
					return nil
				}
				previous.updated = time.Now()
				s.respond(wire.TypeFileResult, wire.FileResult{ItemID: b.ItemID, State: "ready", Offset: previous.file.Written})
				return previous
			}
		}
		dir := ""
		if !b.Clipboard && s.receiveDir != nil {
			dir = s.receiveDir()
		}
		limit := int64(math.MaxInt64)
		if b.Clipboard {
			limit = s.maxFileBytes()
		}
		var clipboard *clipboardReceive
		if b.Clipboard {
			if !s.fileClipboardSupported || s.fileCache == nil || s.captureFileClipboard == nil || frame.sequenceErr != nil {
				s.rejectFile(b.ItemID, "文件剪贴板不可用，请更新双方 Snapover 后重新复制")
				return nil
			}
			publish, protected, err := s.captureFileClipboard(frame.sequence, frame.version)
			if err != nil {
				s.rejectFile(b.ItemID, err.Error())
				return nil
			}
			lease, err := s.fileCache.Begin(protected, limit, b.TotalSize)
			if err != nil {
				s.rejectFile(b.ItemID, err.Error())
				return nil
			}
			clipboard = &clipboardReceive{lease: lease, publish: publish, source: b.SourceApp, limit: limit}
			dir, limit = lease.Dir, filetransfer.BundleLimit(limit)
		}
		fsName := b.Name
		displayName := b.Name
		if clipboard != nil {
			fsName = "contents.zip"
			if displayName == "" {
				displayName = "文件"
			}
		}
		f, err := filetransfer.Begin(dir, fsName, b.TotalSize, limit)
		if err != nil {
			if clipboard != nil {
				clipboard.lease.Release(false)
			}
			s.rejectFile(b.ItemID, err.Error())
			return nil
		}
		current = &fileReceive{resumable: b.Resumable, id: b.ItemID, name: displayName, file: f, clipboard: clipboard, updated: time.Now(), notified: time.Now()}
		s.fileNotice(current, "receiving", "", "正在接收文件")
		s.respond(wire.TypeFileResult, wire.FileResult{ItemID: b.ItemID, State: "ready"})
		return current
	}
	if current == nil {
		if typ == wire.TypeFileCancel && s.fileResumeSupported {
			var cancel wire.ImageEnd
			if wire.Unmarshal(payload, &cancel) == nil {
				if s.resumeFiles != nil {
					if partial, saved := s.resumeFiles.take(s.resumePeer, cancel.ItemID); partial != nil {
						if saved != "" {
							s.resumeFiles.put(s.resumePeer, partial, saved)
							s.respond(wire.TypeFileResult, wire.FileResult{ItemID: cancel.ItemID, State: "complete", Message: saved})
							return nil
						}
						partial.abort()
						s.respond(wire.TypeFileResult, wire.FileResult{ItemID: cancel.ItemID, State: "cancelled"})
						return nil
					}
				}
				s.respond(wire.TypeFileResult, wire.FileResult{ItemID: cancel.ItemID, State: "unknown"})
			}
		}
		return nil
	}
	var err error
	switch typ {
	case wire.TypeFileChunk:
		var c wire.FileChunk
		if wire.Unmarshal(payload, &c) != nil {
			err = errors.New("文件分片无效")
		} else if c.ItemID != current.id {
			return current
		} else if len(c.Data) > wire.ChunkSize {
			err = errors.New("文件分片超过限制")
		} else {
			if current.resumable && len(c.Data) == 0 {
				s.respond(wire.TypeFileResult, wire.FileResult{ItemID: current.id, State: "chunk", Offset: current.file.Written})
			} else {
				err = current.file.Append(c.Offset, c.Data)
				if err == nil && current.resumable {
					s.respond(wire.TypeFileResult, wire.FileResult{ItemID: current.id, State: "chunk", Offset: current.file.Written})
				}
			}
		}
		if err == nil {
			current.updated = time.Now()
			if time.Since(current.notified) >= 100*time.Millisecond {
				s.fileNotice(current, "receiving", "", "正在接收文件")
				current.notified = time.Now()
			}
			return current
		}
	case wire.TypeFileEnd:
		var end wire.FileEnd
		if wire.Unmarshal(payload, &end) != nil {
			err = errors.New("文件结束消息无效")
		} else if end.ItemID != current.id {
			return current
		} else {
			var path string
			path, err = current.file.Finish(end.Hash)
			if err == nil {
				if current.clipboard != nil {
					s.fileNotice(current, "unpacking", "", "正在整理文件")
					err = s.finishFileClipboard(current, path)
					current.clipboard.lease.Release(err == nil || current.clipboard.prepared)
					if err != nil {
						s.rejectFile(end.ItemID, err.Error())
						s.fileNotice(current, "error", "", err.Error())
					} else {
						s.respond(wire.TypeFileResult, wire.FileResult{ItemID: end.ItemID, State: "complete"})
						s.fileNotice(current, "complete", "", "已可粘贴")
					}
					return nil
				}
				if current.resumable {
					s.resumeFiles.put(s.resumePeer, current, filepath.Base(path))
				}
				s.respond(wire.TypeFileResult, wire.FileResult{ItemID: end.ItemID, State: "complete", Message: filepath.Base(path)})
				s.fileNotice(current, "complete", path, "已收到文件")
				return nil
			}
		}
	case wire.TypeFileCancel:
		var cancel wire.ImageEnd
		if wire.Unmarshal(payload, &cancel) != nil || cancel.ItemID != current.id {
			return current
		}
		current.abort()
		s.respond(wire.TypeFileResult, wire.FileResult{ItemID: current.id, State: "cancelled"})
		s.fileNotice(current, "error", "", "对方已停止发送，请重新发送")
		return nil
	}
	current.abort()
	s.rejectFile(current.id, err.Error())
	s.fileNotice(current, "error", "", err.Error())
	return nil
}
