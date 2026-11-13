package sync

import (
	"errors"
	"strings"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

// AppRule is one entry of the application list. ID is the app's stable
// identifier, empty for a rule that only knows a name.
type AppRule struct {
	ID   string
	Name string
}

// SetPrivacyPolicy also invalidates copies queued before the policy changed.
// onlyListed turns the list around: the apps it names are the ones that keep
// being recorded and synced, and every other app is left alone.
func (e *Engine) SetPrivacyPolicy(on bool, rules []AppRule, onlyListed bool) {
	e.mu.Lock()
	e.privacyMode = on
	e.appRules = append([]AppRule(nil), rules...)
	e.onlyListed = onlyListed
	e.privacyEpoch++
	s := e.sess
	e.mu.Unlock()
	if s != nil {
		s.cancelUnsentClipboard(false, "隐私设置已变更，未发送")
	}
}

// blockedLocalLocked reports whether a local copy is left out of history and
// sync. The identifier decides when both sides have one, so that renaming an
// app keeps its rule; otherwise the name does.
func (e *Engine) blockedLocalLocked(item clip.Item) bool {
	if e.privacyMode {
		return true
	}
	listed := e.listedLocked(item)
	if e.onlyListed {
		return !listed
	}
	return listed
}

// listedLocked reports whether the app a copy came from is in the list.
func (e *Engine) listedLocked(item clip.Item) bool {
	for _, rule := range e.appRules {
		if rule.ID != "" && item.SourceID != "" {
			if strings.EqualFold(rule.ID, item.SourceID) {
				return true
			}
			continue
		}
		if name := strings.TrimSpace(rule.Name); name != "" && strings.EqualFold(name, item.SourceApp) {
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
