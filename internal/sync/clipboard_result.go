package sync

import (
	"time"

	"github.com/2fa0/snapover/internal/wire"
)

func (s *Session) deliverClipboardResult(result wire.Ack) {
	s.clipboardResultMu.Lock()
	defer s.clipboardResultMu.Unlock()
	if result.ItemID == s.clipboardSendID && s.clipboardResults != nil {
		select {
		case s.clipboardResults <- result:
		default:
		}
	}
}

// Automatic copies keep only IDs and deadlines, never another payload copy.
func (s *Session) completeClipboard(result wire.Ack) {
	s.mu.Lock()
	_, pending := s.clipboardPending[result.ItemID]
	delete(s.clipboardPending, result.ItemID)
	s.mu.Unlock()
	s.wakeClipboard()
	if pending && s.onClipboardResult != nil {
		if result.State != "complete" && result.Message == "" {
			result.Message = "对端未确认写入剪贴板，请检查对端后重试"
		}
		s.onClipboardResult(result)
	}
}

func (s *Session) expireClipboard(now time.Time, disconnected bool) {
	s.mu.Lock()
	var expired []string
	for id, deadline := range s.clipboardPending {
		if disconnected || !now.Before(deadline) {
			expired = append(expired, id)
			delete(s.clipboardPending, id)
		}
	}
	s.mu.Unlock()
	message := "等待对端确认超时，请检查对端剪贴板后重试"
	if disconnected {
		message = "连接已断开，未收到对端确认，请检查对端剪贴板后重试"
	}
	for _, id := range expired {
		if s.onClipboardResult != nil {
			s.onClipboardResult(wire.Ack{ItemID: id, State: "error", Message: message})
		}
	}
}

func (s *Session) wakeClipboard() {
	select {
	case s.clipboardWake <- struct{}{}:
	default:
	}
}

// Confirmation deadlines remain active while the network writer is busy.
// No timer runs when there are no outstanding copies.
func (s *Session) watchClipboard() {
	for {
		s.mu.Lock()
		var earliest time.Time
		for _, deadline := range s.clipboardPending {
			if earliest.IsZero() || deadline.Before(earliest) {
				earliest = deadline
			}
		}
		s.mu.Unlock()
		var timer *time.Timer
		var due <-chan time.Time
		if !earliest.IsZero() {
			timer = time.NewTimer(time.Until(earliest))
			due = timer.C
		}
		select {
		case <-s.done:
			if timer != nil {
				timer.Stop()
			}
			s.expireClipboard(time.Now(), true)
			return
		case <-s.clipboardWake:
			if timer != nil {
				timer.Stop()
			}
		case now := <-due:
			s.expireClipboard(now, false)
		}
	}
}

func (s *Session) clipboardSendable(item queuedItem, now time.Time) bool {
	if !item.automatic {
		return true
	}
	s.mu.Lock()
	deadline, pending := s.clipboardPending[item.id]
	s.mu.Unlock()
	// An expired queued copy must not overwrite the peer later.
	return pending && now.Before(deadline)
}
