package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/wire"
	"github.com/oklog/ulid/v2"
)

// SendClipboard sends without changing this machine's clipboard. Success means
// the selected peer confirmed a successful write, rather than merely receipt.
func (e *Engine) SendClipboard(ctx context.Context, peerID string, item clip.Item) error {
	item.ReleaseDecoded()
	if err := ctx.Err(); err != nil {
		return err
	}
	if item.Kind != clip.KindText && item.Kind != clip.KindImage || item.Sensitive || item.Skip != "" {
		return errors.New("不支持发送此剪贴板内容")
	}
	e.mu.Lock()
	s := e.sess
	policy := e.policyLocked()
	e.mu.Unlock()
	if s == nil {
		return errors.New("没有在线设备，请先在 Snapover 中连接设备")
	}
	if s.peerID != peerID {
		return errors.New("在线设备已改变，请确认设备后重新发送")
	}
	if send, msg := localSend(policy, item, true); !send {
		return errors.New(msg)
	}
	if !s.clipboardResultSupported {
		return errors.New("对方版本不支持剪贴板接收确认，请更新对方的 Snapover")
	}
	if !s.clipboardSendMu.TryLock() {
		return errors.New("已有文字或图片正在发送，请等待完成")
	}
	defer s.clipboardSendMu.Unlock()
	id := ulid.Make().String()
	results := make(chan wire.Ack, 1)
	s.clipboardResultMu.Lock()
	s.clipboardSendID, s.clipboardResults = id, results
	s.clipboardResultMu.Unlock()
	defer func() {
		s.clipboardResultMu.Lock()
		s.clipboardSendID, s.clipboardResults = "", nil
		s.clipboardResultMu.Unlock()
	}()
	if !s.enqueueID(item, id) {
		return errors.New("发送队列已满或连接已断开，请稍后重试")
	}
	select {
	case result := <-results:
		if result.State != "complete" {
			if result.Message != "" {
				return errors.New(result.Message)
			}
			return errors.New("对端未确认写入剪贴板")
		}
		e.notify(Notice{Kind: NoticeLocal, Item: item, Message: "已发送到对端剪贴板"})
		return nil
	case <-s.done:
		return errors.New("连接已断开，未收到对端确认，请检查对端剪贴板")
	case <-ctx.Done():
		// Discard queued requests and unblock an in-flight write. A canceled CLI
		// must not send later after a reconnection or a slow transport recovers.
		s.Close()
		return fmt.Errorf("未收到对端确认，请检查对端剪贴板：%w", ctx.Err())
	}
}
