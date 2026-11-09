package sync

import (
	"errors"
	"strings"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

// SetPrivacyPolicy also invalidates copies queued before the policy changed.
func (e *Engine) SetPrivacyPolicy(on bool, apps []string) {
	e.mu.Lock()
	e.privacyMode = on
	e.excludedApps = append([]string(nil), apps...)
	e.privacyEpoch++
	s := e.sess
	e.mu.Unlock()
	if s != nil {
		s.cancelUnsentClipboard(false, "隐私设置已变更，未发送")
	}
}

func (e *Engine) blockedLocalLocked(item clip.Item) bool {
	if e.privacyMode {
		return true
	}
	for _, app := range e.excludedApps {
		if name := strings.TrimSpace(app); name != "" && strings.EqualFold(name, item.SourceApp) {
			return true
		}
	}
	return false
}

func (e *Engine) suppressLocal(seq uint64, item clip.Item) bool {
	e.mu.Lock()
	blocked := e.blockedLocalLocked(item)
	if !blocked {
		e.mu.Unlock()
		return false
	}
	e.localActive = time.Now()
	// A suppressed generation cannot become public when the mode is turned off.
	e.haveLast, e.lastSeq = true, seq
	r := e.copyRequest
	if r != nil && seq > r.after && !r.cancelled && time.Now().Before(r.until) {
		close(r.claimed)
		e.copyRequest = nil
	} else {
		r = nil
	}
	e.mu.Unlock()
	if r != nil {
		r.result <- errors.New("隐私设置已阻止记录和同步")
	}
	return true
}
