package sync

import "time"

import "github.com/2fa0/snapover/internal/clip"
import "github.com/2fa0/snapover/internal/lan"

const (
	NoticeLocal   = "local"
	NoticeRemote  = "remote"
	NoticeStatus  = "status"
	NoticeError   = "error"
	NoticeFile    = "file"
	NoticePairing = "pairing"
	NoticeSync    = "clipboard-result"
	NoticeDial    = "dial"
)

// Notice is a clipboard or connection change the window can show.
// Item is set for local and remote clipboard events.
type Notice struct {
	At             time.Time
	Kind           string
	Message        string
	Item           clip.Item
	PeerID         string
	PeerName       string
	Platform       string
	Addr           string
	FileName       string
	FilePath       string
	FileState      string
	FileDone       int64
	FileTotal      int64
	ClipboardState string
	SessionID      string
	Route          lan.Route
	DialSeq        uint64
}

// Link is the peer of the live session, if there is one.
type Link struct {
	SessionID    string
	ID           string
	Name         string
	Platform     string
	Addr         string
	FileTransfer bool
	Route        lan.Route
}

func (e *Engine) notify(n Notice) error {
	e.mu.Lock()
	fn := e.OnNotice
	e.mu.Unlock()
	if fn == nil {
		return nil
	}
	if n.At.IsZero() {
		n.At = time.Now()
	}
	return fn(n)
}

// Link reports the peer of the current session.
func (e *Engine) Link() (Link, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sess == nil || e.link.ID == "" {
		return Link{}, false
	}
	return e.link, true
}
