package sync

import (
	"context"

	"github.com/2fa0/snapover/internal/clip"
)

const (
	localQueueSize  = 8
	localQueueBytes = clip.MaxImageBytes
)

type localJob struct {
	seq   uint64
	item  clip.Item
	bytes int
}

// StartLocalQueue processes settled clipboard events away from the watcher.
// History and UI work must not make the watcher miss an intermediate copy.
func (e *Engine) StartLocalQueue(ctx context.Context) {
	e.localOnce.Do(func() {
		e.localQueue = make(chan localJob, localQueueSize)
		go e.localLoop(ctx)
	})
}

// ObserveLocal enqueues a settled local clipboard event. A full queue is
// reported instead of blocking the watcher.
func (e *Engine) ObserveLocal(seq uint64, item clip.Item) {
	if e.isOwnEcho(seq, item.Hash) || e.ignoreLocalCopy(seq, item) {
		return
	}
	// Local history and transport only need encoded bytes. The transient decode
	// is reserved for a received image's next native clipboard publication.
	item.ReleaseDecoded()
	if e.localQueue == nil {
		e.HandleLocal(seq, item)
		return
	}
	n := queuedSize(item)
	e.mu.Lock()
	full := e.localClosed || n > localQueueBytes-e.localBytes || len(e.localQueue) == cap(e.localQueue)
	if !full {
		e.localBytes += n
	}
	e.mu.Unlock()
	if full {
		e.notify(Notice{Kind: NoticeError, Message: "剪贴板处理队列已满，这次复制未保存"})
		return
	}
	select {
	case e.localQueue <- localJob{seq: seq, item: item, bytes: n}:
	default:
		e.mu.Lock()
		e.localBytes -= n
		if e.localBytes < 0 {
			e.localBytes = 0
		}
		e.mu.Unlock()
		e.notify(Notice{Kind: NoticeError, Message: "剪贴板处理队列已满，这次复制未保存"})
	}
}

func (e *Engine) localLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			e.mu.Lock()
			e.localClosed = true
			e.mu.Unlock()
			return
		case job := <-e.localQueue:
			e.HandleLocal(job.seq, job.item)
			e.mu.Lock()
			e.localBytes -= job.bytes
			if e.localBytes < 0 {
				e.localBytes = 0
			}
			e.mu.Unlock()
		}
	}
}
