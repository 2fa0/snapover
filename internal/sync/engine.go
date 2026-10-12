package sync

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/2fa0/clipbridge/internal/clip"
	"github.com/2fa0/clipbridge/internal/identity"
	"github.com/2fa0/clipbridge/internal/wire"
)

const localPriority = 2 * time.Second

// Engine ties one clipboard to at most one live peer.
type Engine struct {
	LocalID     string
	Name        string
	Platform    string
	Fingerprint string
	TLS         *tls.Config
	Peers       *Store
	Board       clip.Board
	Guard       *clip.EchoGuard
	Log         *slog.Logger
	// OnNotice is called without engine locks, from the goroutine that
	// observed the change. It may be nil.
	OnNotice func(Notice)

	clipMu      sync.Mutex
	mu          sync.Mutex
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
	e.mu.Unlock()

	msg := "已发送"
	switch {
	case sess == nil:
		e.Log.Info("未连接，未发送", "kind", item.Kind, "bytes", item.Size)
		msg = "已复制，未连接"
	case !sess.enqueue(item):
		e.Log.Info("发送队列已满，丢弃", "kind", item.Kind, "bytes", item.Size)
		msg = "发送队列已满"
	default:
		e.Log.Info("已发送", "kind", item.Kind, "bytes", item.Size)
	}
	e.notify(Notice{Kind: NoticeLocal, Item: item, Message: msg})
}

func (e *Engine) deliver(item clip.Item) {
	e.mu.Lock()
	active := time.Since(e.localActive) < localPriority
	e.mu.Unlock()
	if active {
		e.Log.Info("本地刚复制过，不覆盖剪贴板", "kind", item.Kind, "bytes", item.Size)
		e.notify(Notice{Kind: NoticeStatus, Message: "本地刚复制过，未覆盖剪贴板"})
		return
	}

	e.clipMu.Lock()
	e.Guard.Mark(item.Hash)
	err := e.Board.Write(item)
	if err == nil {
		if back, snapErr := e.Board.Snapshot(); snapErr == nil && back.Kind != "" {
			e.Guard.Mark(back.Hash)
		}
		if seq, seqErr := e.Board.Sequence(); seqErr == nil {
			e.mu.Lock()
			e.suppressSeq = seq
			e.mu.Unlock()
		}
	}
	e.clipMu.Unlock()
	if err != nil {
		e.Log.Error("写入剪贴板失败", "err", err)
		e.notify(Notice{Kind: NoticeError, Message: "写入剪贴板失败"})
		return
	}
	e.Log.Info("已写入剪贴板", "kind", item.Kind, "bytes", item.Size)
	link, _ := e.Link()
	e.notify(Notice{Kind: NoticeRemote, Item: item, Message: "已写入剪贴板", PeerID: link.ID, PeerName: link.Name})
}

// Connected reports whether a peer session is up.
func (e *Engine) Connected() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sess != nil
}

// Attach runs Hello, pins the peer certificate, and serves the connection
// until it closes. weDialed is true when this process opened the TCP connection.
func (e *Engine) Attach(conn *tls.Conn, weDialed bool) error {
	defer conn.Close()
	peer, hello, err := e.handshake(conn)
	if err != nil {
		return err
	}
	addr := conn.RemoteAddr().String()
	first, err := e.Peers.Allow(peer.id, peer.fp, hello.Name, hello.Platform, addr)
	if err != nil {
		if errors.Is(err, ErrFingerprintMismatch) {
			e.Log.Error("对端证书变了，拒绝连接。若对端重装过，删除 peers.json 中该设备或加 -reset-peers", "peer", peer.id, "fp", identity.FormatFingerprint(peer.fp))
			e.notify(Notice{Kind: NoticeError, Message: "对端证书变了，已拒绝。可在设置里清除信任后再连", PeerID: peer.id})
		} else {
			e.Log.Error("信任对端失败", "err", err)
			e.notify(Notice{Kind: NoticeError, Message: "信任对端失败"})
		}
		return err
	}
	if first {
		e.Log.Warn("首次信任对端。请和对端启动日志里的指纹核对", "peer", peer.id, "name", hello.Name, "fp", identity.FormatFingerprint(peer.fp))
	}

	sess := newSession(conn, peer.id, weDialed, e.Log, e.deliver)
	if !e.adopt(sess) {
		e.Log.Info("关掉重复连接", "peer", peer.id, "dialed", weDialed)
		return nil
	}
	e.mu.Lock()
	e.link = Link{ID: peer.id, Name: hello.Name, Platform: hello.Platform, Addr: addr}
	e.mu.Unlock()
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
	e.drop(sess)
	e.Log.Info("连接断开", "peer", peer.id)
	e.notify(Notice{Kind: NoticeStatus, Message: "连接断开", PeerID: peer.id, PeerName: hello.Name})
	return nil
}

func (e *Engine) handshake(conn *tls.Conn) (*identityResult, wire.Hello, error) {
	hello := wire.Hello{
		DeviceID:    e.LocalID,
		Name:        e.Name,
		Platform:    e.Platform,
		Proto:       wire.Proto,
		Fingerprint: e.Fingerprint,
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

// adopt keeps a single session. When both sides dial, the connection opened
// by the smaller device id wins; the other is closed.
func (e *Engine) adopt(s *Session) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur := e.sess
	if cur == nil || cur.peerID != s.peerID {
		if cur != nil {
			cur.Close()
		}
		e.sess = s
		return true
	}
	newWins := keep(e.LocalID, s.peerID, s.weDialed) && !keep(e.LocalID, s.peerID, cur.weDialed)
	if newWins {
		cur.Close()
		e.sess = s
		return true
	}
	s.Close()
	return false
}

func (e *Engine) drop(s *Session) {
	e.mu.Lock()
	if e.sess == s {
		e.sess = nil
		e.link = Link{}
	}
	e.mu.Unlock()
	s.Close()
}

// keep is true when this side is playing the role the tie-break assigned.
// The smaller device id is the designated dialer.
func keep(local, peer string, weDialed bool) bool {
	return weDialed == (local < peer)
}
