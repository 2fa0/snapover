package sync

import "time"

import "github.com/2fa0/clipbridge/internal/clip"

const (
	NoticeLocal  = "local"
	NoticeRemote = "remote"
	NoticeStatus = "status"
	NoticeError  = "error"
)

// Notice is a clipboard or connection change the window can show.
// Item is set for local and remote clipboard events.
type Notice struct {
	At       time.Time
	Kind     string
	Message  string
	Item     clip.Item
	PeerID   string
	PeerName string
	Platform string
	Addr     string
}

// Link is the peer of the live session, if there is one.
type Link struct {
	ID       string
	Name     string
	Platform string
	Addr     string
}

func (e *Engine) notify(n Notice) {
	e.mu.Lock()
	fn := e.OnNotice
	e.mu.Unlock()
	if fn == nil {
		return
	}
	if n.At.IsZero() {
		n.At = time.Now()
	}
	fn(n)
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
