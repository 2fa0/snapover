package sync

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/wire"
)

const localPriority = 2 * time.Second

var errHistoryOnly = errors.New("对端刚复制过内容，已存入对端历史，未覆盖剪贴板")

// Engine ties one clipboard to at most one live peer.
type Engine struct {
	LocalID     string
	Name        string
	Platform    string
	Fingerprint string
	ListenPort  string
	TLS         *tls.Config
	Peers       *Store
	Board       clip.Board
	Guard       *clip.EchoGuard
	Log         *slog.Logger
	// OnNotice is called without engine locks, from the goroutine that
	// observed the change. Clipboard events return history storage errors.
	// It may be nil.
	OnNotice func(Notice) error
	// RunOnBoard runs a write synchronously on the clipboard's owning thread.
	// It may be nil for boards that can write directly from a goroutine.
	RunOnBoard func(func())

	authMu        sync.Mutex
	authEpoch     uint64
	authConns     map[*tls.Conn]string
	pairingUntil  time.Time
	pairLast      time.Time
	pairLastID    string
	pairPreferred bool
	pairSerial    uint64
	pendingPair   *pairRequest

	clipMu      sync.Mutex
	mu          sync.Mutex
	paused      bool
	textOnly    bool
	maxImage    int
	maxText     int
	receiveDir  string
	sess        *Session
	link        Link
	localActive time.Time
	lastHash    [32]byte
	haveLast    bool
	suppressSeq uint64
}

// HandleLocal is called for a clipboard change that happened on this machine.
func (e *Engine) HandleLocal(seq uint64, item clip.Item) {
	e.mu.Lock()
	if seq != 0 && seq == e.suppressSeq {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()

	if item.Sensitive {
		e.Log.Info("跳过敏感内容", "mark", item.Skip)
		e.notify(Notice{Kind: NoticeStatus, Message: "跳过敏感内容"})
		return
	}
	if item.Skip != "" {
		e.Log.Info("跳过剪贴板", "reason", item.Skip)
		e.notify(Notice{Kind: NoticeStatus, Message: "跳过：" + item.Skip})
		return
	}
	if item.Kind == "" {
		return
	}
	if item.Kind == clip.KindText {
		if err := clip.CheckTextSize(item.Text, e.TextLimit()); err != nil {
			e.Log.Info("跳过剪贴板", "reason", err.Error())
			e.notify(Notice{Kind: NoticeStatus, Message: "跳过：" + err.Error()})
			return
		}
	}
	if e.Guard.IsEcho(item.Hash) {
		return
	}

	e.mu.Lock()
	if e.haveLast && e.lastHash == item.Hash {
		e.mu.Unlock()
		return
	}
	e.lastHash = item.Hash
	e.haveLast = true
	e.localActive = time.Now()
	sess := e.sess
	policy := e.policyLocked()
	e.mu.Unlock()

	send, msg := localSend(policy, item, sess != nil)
	switch {
	case !send:
		e.Log.Info(msg, "kind", item.Kind, "bytes", item.Size)
	case !sess.enqueue(item):
		e.Log.Info("发送队列已满，丢弃", "kind", item.Kind, "bytes", item.Size)
		msg = "发送队列已满"
	default:
		e.Log.Info("已发送", "kind", item.Kind, "bytes", item.Size)
	}
	e.notify(Notice{Kind: NoticeLocal, Item: item, Message: msg})
}

// SetPolicy updates the options the settings page can change.
func (e *Engine) SetPolicy(paused, textOnly bool, maxImage int) {
	e.mu.Lock()
	e.paused = paused
	e.textOnly = textOnly
	e.maxImage = maxImage
	e.mu.Unlock()
}

func (e *Engine) policyLocked() policy {
	return policy{Paused: e.paused, TextOnly: e.textOnly, MaxImage: e.maxImage, MaxText: e.textLimitLocked()}
}

func (e *Engine) SetTextLimit(maxBytes int) {
	if maxBytes <= 0 {
		maxBytes = clip.DefaultMaxTextBytes
	}
	if maxBytes > clip.MaxTextBytes {
		maxBytes = clip.MaxTextBytes
	}
	e.mu.Lock()
	e.maxText = maxBytes
	e.mu.Unlock()
}

func (e *Engine) textLimitLocked() int {
	if e.maxText == 0 {
		return clip.DefaultMaxTextBytes
	}
	return e.maxText
}

func (e *Engine) TextLimit() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.textLimitLocked()
}

type policy struct {
	Paused   bool
	TextOnly bool
	MaxImage int
	MaxText  int
}

// localSend decides whether a local clipboard item is pushed to the peer.
// Items that are not sent are still recorded in history by the caller.
func localSend(p policy, item clip.Item, connected bool) (bool, string) {
	if item.Kind == clip.KindText {
		limit := p.MaxText
		if limit == 0 {
			limit = clip.DefaultMaxTextBytes
		}
		if err := clip.CheckTextSize(item.Text, limit); err != nil {
			return false, err.Error()
		}
	}
	switch {
	case p.Paused:
		return false, "已暂停，未发送"
	case p.TextOnly && item.Kind == clip.KindImage:
		return false, "只同步文本，图片未发送"
	case item.Kind == clip.KindImage && p.MaxImage > 0 && item.Size > p.MaxImage:
		return false, "图片超过上限，已记入历史，未同步"
	case !connected:
		return false, "已复制，未连接"
	default:
		return true, "已发送"
	}
}

func (e *Engine) deliver(item clip.Item) {
	e.deliverFrom(nil, item)
}

func (e *Engine) deliverFrom(source *Session, item clip.Item) error {
	e.clipMu.Lock()
	var paused, active, attempted bool
	var link Link
	var err error
	var textErr error
	write := func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if source != nil && e.sess != source {
			return
		}
		paused = e.paused
		active = time.Since(e.localActive) < localPriority
		if paused {
			return
		}
		if item.Kind == clip.KindText {
			textErr = clip.CheckTextSize(item.Text, e.textLimitLocked())
			if textErr != nil {
				return
			}
		}
		link = e.link
		if active {
			return
		}
		attempted = true
		e.Guard.Mark(item.Hash)
		err = e.Board.Write(item)
	}
	// Validate and write in the same UI callback: revocation must neither
	// wait for a queued UI write nor allow it to run after the session is gone.
	if e.RunOnBoard != nil {
		e.RunOnBoard(write)
	} else {
		write()
	}
	if paused {
		e.clipMu.Unlock()
		e.Log.Info("已暂停，忽略对端内容", "kind", item.Kind, "bytes", item.Size)
		e.notify(Notice{Kind: NoticeStatus, Message: "已暂停，忽略对端内容"})
		return errors.New("对端已暂停同步，未写入剪贴板")
	}
	if textErr != nil {
		e.clipMu.Unlock()
		e.notify(Notice{Kind: NoticeStatus, Message: "未接收：" + textErr.Error()})
		return fmt.Errorf("对端未接收：%w", textErr)
	}
	if active {
		e.clipMu.Unlock()
		e.Log.Info("本地刚复制过，对端内容仅保存历史", "kind", item.Kind, "bytes", item.Size)
		if err := e.notify(Notice{Kind: NoticeRemote, Item: item, PeerID: link.ID, PeerName: link.Name}); err != nil {
			return fmt.Errorf("对端保存历史失败，未写入剪贴板：%w", err)
		}
		return errHistoryOnly
	}
	if !attempted {
		e.clipMu.Unlock()
		return errors.New("连接已改变，未写入剪贴板")
	}
	if err == nil {
		if back, snapErr := e.Board.Snapshot(); snapErr == nil && back.Kind != "" {
			e.Guard.Mark(back.Hash)
		}
		if seq, seqErr := e.Board.Sequence(); seqErr == nil {
			e.mu.Lock()
			if source == nil || e.sess == source {
				e.suppressSeq = seq
			}
			e.mu.Unlock()
		}
	}
	e.clipMu.Unlock()
	if err != nil {
		e.Log.Error("写入剪贴板失败", "err", err)
		e.notify(Notice{Kind: NoticeError, Message: "写入剪贴板失败"})
		return errors.New("对端写入剪贴板失败，请检查对端剪贴板后重试")
	}
	e.Log.Info("已写入剪贴板", "kind", item.Kind, "bytes", item.Size)
	e.notify(Notice{Kind: NoticeRemote, Item: item, Message: "已写入剪贴板", PeerID: link.ID, PeerName: link.Name})
	return nil
}

// Connected reports whether a peer session is up.
func (e *Engine) Connected() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sess != nil
}

// Attach authenticates a peer before creating any clipboard or file session.
func (e *Engine) Attach(conn *tls.Conn, weDialed bool) error {
	return e.AttachPeer(conn, weDialed, "")
}

// AttachPeer also checks the identity selected in the device list. Discovery
// advertisements and saved addresses are routing hints, never proof of identity.
func (e *Engine) AttachPeer(conn *tls.Conn, weDialed bool, expectedID string) error {
	// Rejection must release the transport immediately; a TLS close-notify
	// can otherwise wait for a peer that has already stopped reading.
	defer conn.NetConn().Close()
	e.authMu.Lock()
	epoch := e.authEpoch
	if e.authConns == nil {
		e.authConns = make(map[*tls.Conn]string)
	}
	e.authConns[conn] = ""
	e.authMu.Unlock()
	defer func() {
		e.authMu.Lock()
		delete(e.authConns, conn)
		e.authMu.Unlock()
	}()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	peer, hello, err := e.handshake(conn)
	if err != nil {
		return err
	}
	if expectedID != "" && peer.id != expectedID {
		return ErrWrongPeer
	}
	e.authMu.Lock()
	e.authConns[conn] = peer.id
	e.authMu.Unlock()
	paired, err := e.authorizePair(conn, peer, hello, epoch, weDialed)
	if err != nil {
		if errors.Is(err, ErrFingerprintMismatch) {
			e.notify(Notice{Kind: NoticeError, Message: "设备身份发生变化，已拒绝连接。确认对端重装后，解除配对再重新添加", PeerID: peer.id})
		} else {
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = wire.WriteMsg(conn, wire.TypeError, wire.Error{Code: 2, Msg: err.Error()})
			e.notify(Notice{Kind: NoticeError, Message: err.Error(), PeerID: peer.id})
		}
		return err
	}
	addr := conn.RemoteAddr().String()
	for _, cap := range hello.Capabilities {
		if cap == wire.PairCapability {
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			var ready wire.PairChoice
			if err := exchangePair(conn, wire.TypePairReady, wire.PairChoice{Accept: true}, &ready); err != nil {
				return err
			}
			if !ready.Accept {
				return ErrPairingStopped
			}
		}
	}
	_ = conn.SetDeadline(time.Time{})

	sess := newSession(conn, peer.id, weDialed, e.Log, nil)
	sess.onItem = func(item clip.Item) error { return e.deliverFrom(sess, item) }
	for _, cap := range hello.Capabilities {
		if cap == wire.FileCapability {
			sess.fileSupported = true
		}
		if cap == wire.ClipboardResultCapability {
			sess.clipboardResultSupported = true
		}
	}
	sess.receiveDir = e.ReceiveDir
	sess.onFile = func(n Notice) { n.PeerID = peer.id; n.PeerName = hello.Name; e.notify(n) }
	e.authMu.Lock()
	if epoch != e.authEpoch {
		e.authMu.Unlock()
		return ErrPairingStopped
	}
	_, err = e.Peers.Allow(peer.id, peer.fp, hello.Name, hello.Platform, reconnectAddr(addr, weDialed, hello.ListenPort))
	if err != nil {
		e.authMu.Unlock()
		e.notify(Notice{Kind: NoticeError, Message: "保存配对失败，请检查数据目录权限后重试", PeerID: peer.id})
		return err
	}
	if paired {
		e.pairingUntil = time.Time{}
	}
	adopted := e.adopt(sess, Link{ID: peer.id, Name: hello.Name, Platform: hello.Platform, Addr: addr, FileTransfer: sess.fileSupported})
	e.authMu.Unlock()
	if !adopted {
		e.Log.Info("关掉重复连接", "peer", peer.id, "dialed", weDialed)
		return nil
	}
	e.Log.Info("已连接", "peer", peer.id, "name", hello.Name, "platform", hello.Platform, "dialed", weDialed)
	e.notify(Notice{
		Kind:     NoticeStatus,
		Message:  "已连接 " + hello.Name,
		PeerID:   peer.id,
		PeerName: hello.Name,
		Platform: hello.Platform,
		Addr:     addr,
	})
	sess.run()
	if e.drop(sess) {
		e.Log.Info("连接断开", "peer", peer.id)
		e.notify(Notice{Kind: NoticeStatus, Message: "设备离线，等待重连", PeerID: peer.id, PeerName: hello.Name})
	}
	return nil
}

func (e *Engine) handshake(conn *tls.Conn) (*identityResult, wire.Hello, error) {
	hello := wire.Hello{
		DeviceID:     e.LocalID,
		Name:         e.Name,
		Platform:     e.Platform,
		Proto:        wire.Proto,
		Fingerprint:  e.Fingerprint,
		ListenPort:   e.ListenPort,
		Capabilities: []string{wire.FileCapability, wire.PairCapability, wire.ClipboardResultCapability},
	}
	errCh := make(chan error, 1)
	go func() { errCh <- wire.WriteMsg(conn, wire.TypeHello, hello) }()
	typ, payload, err := wire.ReadFrame(conn)
	werr := <-errCh
	if err != nil {
		return nil, wire.Hello{}, err
	}
	if werr != nil {
		return nil, wire.Hello{}, werr
	}
	if typ != wire.TypeHello {
		return nil, wire.Hello{}, errors.New("对端第一帧不是 Hello")
	}
	var peerHello wire.Hello
	if err := wire.Unmarshal(payload, &peerHello); err != nil {
		return nil, wire.Hello{}, err
	}
	if peerHello.Proto != wire.Proto {
		_ = wire.WriteMsg(conn, wire.TypeError, wire.Error{Code: 1, Msg: "protocol mismatch"})
		return nil, wire.Hello{}, errors.New("协议版本不一致")
	}
	peer, err := peerCert(conn)
	if err != nil {
		return nil, wire.Hello{}, err
	}
	if peer.id == e.LocalID {
		return nil, wire.Hello{}, errors.New("连到了自己")
	}
	if peerHello.DeviceID != peer.id {
		return nil, wire.Hello{}, errors.New("Hello 的设备 id 和证书不一致")
	}
	return peer, peerHello, nil
}

// Incoming connections use a temporary source port, not the peer's listener.
// Older peers omit ListenPort and use the default port.
func reconnectAddr(remote string, weDialed bool, listenPort string) string {
	if weDialed {
		return remote
	}
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return ""
	}
	if listenPort == "" {
		listenPort = "47821"
	}
	port, err := strconv.Atoi(listenPort)
	if err != nil || port < 1 || port > 65535 {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// adopt keeps a single session. When both sides dial, the connection opened
// by the smaller device id wins; the other is closed.
func (e *Engine) adopt(s *Session, link Link) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur := e.sess
	if cur == nil || cur.peerID != s.peerID {
		if cur != nil {
			cur.Close()
		}
		e.sess = s
		e.link = link
		return true
	}
	newWins := keep(e.LocalID, s.peerID, s.weDialed) && !keep(e.LocalID, s.peerID, cur.weDialed)
	if newWins {
		cur.Close()
		e.sess = s
		e.link = link
		return true
	}
	s.Close()
	return false
}

func (e *Engine) drop(s *Session) bool {
	e.mu.Lock()
	current := e.sess == s
	if current {
		e.sess = nil
		e.link = Link{}
	}
	e.mu.Unlock()
	s.Close()
	return current
}

// keep is true when this side is playing the role the tie-break assigned.
// The smaller device id is the designated dialer.
func keep(local, peer string, weDialed bool) bool {
	return weDialed == (local < peer)
}
