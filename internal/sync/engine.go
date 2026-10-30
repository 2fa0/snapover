package sync

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/filetransfer"
	"github.com/2fa0/snapover/internal/lan"
	"github.com/2fa0/snapover/internal/wire"
	"github.com/oklog/ulid/v2"
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
	FileCache  *filetransfer.ClipboardCache

	authMu              sync.Mutex
	authEpoch           uint64
	authConns           map[*tls.Conn]string
	pairingUntil        time.Time
	pairLast            time.Time
	pairLastID          string
	pairPreferred       bool
	pairSerial          uint64
	pendingPair         *pairRequest
	allowTailscale      bool
	tailscaleEpoch      uint64
	tailscaleConns      map[*tls.Conn]struct{}
	tailscaleDials      map[uint64]tailscaleDial
	nextNetworkDial     uint64
	tailscaleRemovalSeq uint64
	tailscaleRemovals   map[string]uint64

	clipMu      sync.Mutex
	mu          sync.Mutex
	paused      bool
	manualSync  bool
	copyRequest *copyRequest
	ignoreNext  bool
	ignoreAfter uint64
	ignoredSeq  uint64
	haveIgnored bool
	textOnly    bool
	maxImage    int
	maxText     int
	maxFile     int64
	receiveDir  string
	sess        *Session
	link        Link
	localActive time.Time
	lastHash    [32]byte
	lastSeq     uint64
	haveLast    bool
	lastSendID  string
	ownSeq      uint64
	ownHash     [32]byte
	haveOwnSeq  bool
	localOnce   sync.Once
	localQueue  chan localJob
	localBytes  int
	localClosed bool
}

// HandleLocal is called for a clipboard change that happened on this machine.
func (e *Engine) HandleLocal(seq uint64, item clip.Item) {
	if e.isOwnEcho(seq, item.Hash) || e.ignoreLocalCopy(seq, item) {
		return
	}
	e.mu.Lock()
	r := e.copyRequest
	if r != nil && (seq <= r.after || r.cancelled || !time.Now().Before(r.until)) {
		if r != nil && seq <= r.after {
			// The shortcut is waiting for a newer generation. The clipboard it
			// started from is not a new copy, even when the bytes match a recent item.
			e.mu.Unlock()
			return
		}
		r = nil
	} else if r != nil {
		close(r.claimed)
		e.copyRequest = nil
	}
	e.mu.Unlock()
	err := e.handleLocal(item, r, seq)
	if r != nil {
		r.result <- err
	}
}

// SetIgnoreNextCopy arms a single settled local copy, never an event already
// queued when the user chose the action. The setting does not survive restart.
func (e *Engine) SetIgnoreNextCopy(on bool) error {
	var seq uint64
	if on {
		if e.Board == nil {
			return errors.New("剪贴板尚未就绪")
		}
		var err error
		seq, err = e.Board.Sequence()
		if err != nil {
			return err
		}
	}
	e.mu.Lock()
	e.ignoreNext, e.ignoreAfter = on, seq
	e.mu.Unlock()
	return nil
}

func (e *Engine) IgnoreNextCopyPending() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ignoreNext
}

// Both capture and direct callers check this before queueing/history work.
// Queue saturation must not turn a private copy into the next public event.
func (e *Engine) ignoreLocalCopy(seq uint64, item clip.Item) bool {
	e.mu.Lock()
	if e.haveIgnored && seq == e.ignoredSeq {
		e.mu.Unlock()
		return true
	}
	if !e.ignoreNext || seq <= e.ignoreAfter || item.LocalWrite {
		e.mu.Unlock()
		return false
	}
	e.ignoreNext, e.haveIgnored, e.ignoredSeq = false, true, seq
	e.localActive = time.Now()
	r := e.copyRequest
	if r != nil && seq > r.after && !r.cancelled && time.Now().Before(r.until) {
		close(r.claimed)
		e.copyRequest = nil
	} else {
		r = nil
	}
	e.mu.Unlock()
	err := errors.New("已忽略这次复制，未记录或同步")
	if r != nil {
		r.result <- err
	}
	e.notify(Notice{Kind: NoticeStatus, Message: err.Error()})
	return true
}

// isOwnEcho is true only for a generation this process just wrote.
// A later copy of the same bytes is a new event.
func (e *Engine) isOwnEcho(seq uint64, hash [32]byte) bool {
	if e.Board != nil {
		if owner, ok := e.Board.(interface{ OwnsSequence(uint64) bool }); ok && owner.OwnsSequence(seq) {
			return true
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.haveOwnSeq && seq == e.ownSeq && e.ownHash == hash
}

func (e *Engine) handleLocal(item clip.Item, request *copyRequest, seq uint64) error {
	if item.Sensitive {
		e.Log.Info("跳过敏感内容", "mark", item.Skip)
		e.notify(Notice{Kind: NoticeStatus, Message: "跳过敏感内容"})
		return errors.New("敏感内容未发送")
	}
	if item.Skip != "" {
		e.Log.Info("跳过剪贴板", "reason", item.Skip)
		e.notify(Notice{Kind: NoticeStatus, Message: "跳过：" + item.Skip})
		return errors.New(item.Skip)
	}
	if item.Kind == "" {
		return errors.New("没有可同步的文本、图片或文件")
	}
	if item.Kind == clip.KindText {
		if err := clip.CheckTextSize(item.Text, e.TextLimit()); err != nil {
			e.Log.Info("跳过剪贴板", "reason", err.Error())
			e.notify(Notice{Kind: NoticeStatus, Message: "跳过：" + err.Error()})
			return err
		}
	}
	e.mu.Lock()
	// Same generation can be delivered twice; same bytes on a new generation cannot.
	if request == nil && e.haveLast && e.lastSeq == seq {
		e.mu.Unlock()
		return nil
	}
	e.lastHash = item.Hash
	e.lastSeq = seq
	e.haveLast = true
	id := ulid.Make().String()
	if item.Kind == clip.KindFiles {
		item.CopySequence = seq
	}
	e.lastSendID = id
	e.localActive = time.Now()
	sess := e.sess
	policy := e.policyLocked()
	manual := e.manualSync
	e.mu.Unlock()

	send, msg := localSend(policy, item, sess != nil)
	if send && item.Kind == clip.KindFiles && !sess.fileClipboardSupported {
		send, msg = false, "对方版本不支持文件剪贴板，请更新对方的 Snapover"
	}
	if request != nil && request.cancelled {
		e.allowLocalRetry(sess, id)
		e.notify(Notice{Kind: NoticeLocal, Item: item})
		return errors.New("复制处理超时，已保留在本机历史，请稍后查看")
	}
	if request == nil && manual {
		e.notify(Notice{Kind: NoticeLocal, Item: item})
		return nil
	}
	if request != nil && sess != request.session {
		send, msg = false, "连接已改变，请重新复制并同步"
	}
	switch {
	case !send:
		e.allowLocalRetry(sess, id)
		e.Log.Info(msg, "kind", item.Kind, "bytes", item.Size)
	case !sess.enqueueClipboard(item, id, true, request != nil):
		e.allowLocalRetry(sess, id)
		e.Log.Info("发送队列已满，丢弃", "kind", item.Kind, "bytes", item.Size)
		msg = "发送队列已满"
	default:
		msg = ""
		e.Log.Info("剪贴板已入队", "item", id, "kind", item.Kind, "bytes", item.Size)
	}
	e.notify(Notice{Kind: NoticeLocal, Item: item, Message: msg})
	if msg != "" {
		return errors.New(msg)
	}
	return nil
}

func (e *Engine) allowLocalRetry(source *Session, id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sess == source && e.lastSendID == id {
		e.haveLast = false
	}
}

// SetPolicy updates the options the settings page can change.
func (e *Engine) SetPolicy(paused, textOnly bool, maxImage int) {
	e.mu.Lock()
	e.paused = paused
	e.textOnly = textOnly
	e.maxImage = maxImage
	sess := e.sess
	e.mu.Unlock()
	if paused && sess != nil {
		sess.cancelUnsentClipboard(false, "已暂停，未发送")
	}
}

func (e *Engine) admitClipboard(item queuedItem) (bool, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.paused {
		return false, "已暂停，未发送"
	}
	if e.manualSync && !item.explicit {
		return false, "已改为手动同步，未发送"
	}
	send, msg := localSend(e.policyLocked(), item.Item, true)
	if !send {
		return false, msg
	}
	return true, ""
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
	case p.TextOnly && item.Kind != clip.KindText:
		return false, "只同步文本，图片和文件未发送"
	case item.Kind == clip.KindImage && p.MaxImage > 0 && item.Size > p.MaxImage:
		return false, "图片超过上限，已记入历史，未同步"
	case !connected:
		return false, "已复制，未连接"
	default:
		return true, ""
	}
}

func (e *Engine) deliver(item clip.Item) {
	e.deliverFrom(nil, item)
}

// rejectRemoteBeforePrepare skips image conversion when the item cannot be
// published. The clipboard callback still rechecks in case policy changes.
func (e *Engine) rejectRemoteBeforePrepare(source *Session, item clip.Item) error {
	e.mu.Lock()
	paused := e.paused
	textOnly := e.textOnly
	var textErr error
	if item.Kind == clip.KindText {
		textErr = clip.CheckTextSize(item.Text, e.textLimitLocked())
	}
	disconnected := false
	if source != nil && e.sess != source {
		disconnected = true
	}
	e.mu.Unlock()
	if source != nil && !disconnected {
		select {
		case <-source.done:
			disconnected = true
		default:
		}
	}
	if disconnected {
		return errors.New("连接已改变，未写入剪贴板")
	}
	if paused {
		e.Log.Info("已暂停，忽略对端内容", "kind", item.Kind, "bytes", item.Size)
		e.notify(Notice{Kind: NoticeStatus, Message: "已暂停，忽略对端内容"})
		return errors.New("对端已暂停同步，未写入剪贴板")
	}
	if textOnly && item.Kind == clip.KindFiles {
		return errors.New("对端只同步文本，未接收文件")
	}
	if textErr != nil {
		e.notify(Notice{Kind: NoticeStatus, Message: "未接收：" + textErr.Error()})
		return fmt.Errorf("对端未接收：%w", textErr)
	}
	return nil
}

func (e *Engine) deliverFrom(source *Session, item clip.Item) error {
	return e.deliverFromSequence(source, item, 0, false, 0)
}

func (e *Engine) deliverFromSequence(source *Session, item clip.Item, expected uint64, enforce bool, version uint64) error {
	if err := e.rejectRemoteBeforePrepare(source, item); err != nil {
		item.ReleaseDecoded()
		return err
	}
	e.clipMu.Lock()
	sequence, sequenceErr := e.Board.Sequence()
	haveSequence := sequenceErr == nil
	if enforce {
		sequence, haveSequence = expected, true
	}
	publish := func() error { return e.Board.Write(item) }
	if board, ok := e.Board.(interface {
		PrepareRemoteWrite(clip.Item) (clip.PreparedWrite, error)
	}); ok {
		prepared, prepareErr := board.PrepareRemoteWrite(item)
		defer prepared.Close()
		publish = func() error {
			if prepareErr != nil {
				return prepareErr
			}
			return prepared.Publish()
		}
	}
	item.ReleaseDecoded()
	var paused, active, attempted, fileTextOnly bool
	var link Link
	var err error
	var textErr error
	attempt := 0
	write := func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		attempted = false
		if source != nil {
			if e.sess != source {
				return
			}
			select {
			case <-source.done:
				return
			default:
			}
		}
		paused = e.paused
		fileTextOnly = item.Kind == clip.KindFiles && e.textOnly
		active = time.Since(e.localActive) < localPriority
		if paused || fileTextOnly {
			return
		}
		if item.Kind == clip.KindText {
			textErr = clip.CheckTextSize(item.Text, e.textLimitLocked())
			if textErr != nil {
				return
			}
		}
		link = e.link
		if enforce && source != nil && source.clipboardVersion.Load() != version {
			active = true
		}
		if active {
			return
		}
		seq, seqErr := e.Board.Sequence()
		own := enforce && e.haveOwnSeq && seq == e.ownSeq
		if owner, ok := e.Board.(interface{ OwnsSequence(uint64) bool }); ok && own {
			own = owner.OwnsSequence(seq)
		}
		if haveSequence && (seqErr != nil || seq != sequence) && !(enforce && own) {
			// A new local copy must win even before the watcher has settled it.
			active = true
			return
		}
		attempted = true
		err = publish()
		if err == nil {
			if e.Guard != nil {
				e.Guard.Mark(item.Hash)
			}
			if seq, seqErr := e.Board.Sequence(); seqErr == nil {
				e.ownSeq = seq
				e.ownHash = item.Hash
				e.haveOwnSeq = true
			} else {
				e.haveOwnSeq = false
			}
			// The clipboard is no longer the last local copy.
			e.haveLast = false
		}
	}
	// Validate and write in the same UI callback: revocation must neither
	// wait for a queued UI write nor allow it to run after the session is gone.
	for {
		if e.RunOnBoard != nil {
			e.RunOnBoard(write)
		} else {
			write()
		}
		if !attempted || !clipboardBusy(err) || attempt == 7 {
			break
		}
		attempt++
		// Wait on the receiving goroutine, outside both the UI callback and e.mu.
		timer := time.NewTimer(time.Duration(attempt) * 25 * time.Millisecond)
		var disconnected <-chan struct{}
		if source != nil {
			disconnected = source.done
		}
		select {
		case <-timer.C:
		case <-disconnected:
			timer.Stop()
			e.clipMu.Unlock()
			return errors.New("连接已改变，未写入剪贴板")
		}
	}
	if fileTextOnly {
		e.clipMu.Unlock()
		return errors.New("对端只同步文本，未接收文件")
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
	e.clipMu.Unlock()
	if err != nil {
		e.Log.Error("写入剪贴板失败", "err", err)
		if historyErr := e.notify(Notice{Kind: NoticeRemote, Item: item, PeerID: link.ID, PeerName: link.Name}); historyErr != nil {
			e.Log.Error("保存对端历史失败", "err", historyErr)
			return fmt.Errorf("对端写入剪贴板失败，保存历史也失败：%w", historyErr)
		}
		e.notify(Notice{Kind: NoticeError, Message: "写入剪贴板失败，已保存到历史"})
		return errors.New("对端写入剪贴板失败，已存入对端历史，可稍后重新复制")
	}
	e.Log.Info("已写入剪贴板", "kind", item.Kind, "bytes", item.Size)
	if historyErr := e.notify(Notice{Kind: NoticeRemote, Item: item, PeerID: link.ID, PeerName: link.Name}); historyErr != nil {
		e.Log.Error("保存对端历史失败", "err", historyErr)
		return fmt.Errorf("对端已写入剪贴板，但保存历史失败：%w", historyErr)
	}
	return nil
}

func clipboardBusy(err error) bool {
	// MyGo currently exposes HRESULTs as text rather than a typed error.
	return errors.Is(err, clip.ErrClipboardBusy) || err != nil && strings.Contains(err.Error(), "OleSetClipboard failed (HRESULT 0x800401d0)")
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
	return e.attachPeer(context.Background(), conn, weDialed, expectedID, lan.RouteLAN)
}

func (e *Engine) attachPeer(ctx context.Context, conn *tls.Conn, weDialed bool, expectedID string, route lan.Route) error {
	sequence := dialSequence(ctx)
	// Rejection must release the transport immediately; a TLS close-notify
	// can otherwise wait for a peer that has already stopped reading.
	defer conn.NetConn().Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.NetConn().Close() })
	defer stop()
	e.authMu.Lock()
	if route == lan.RouteTailscale && !e.allowTailscale {
		e.authMu.Unlock()
		return lan.ErrTailscaleDisabled
	}
	epoch := e.authEpoch
	networkEpoch := e.tailscaleEpoch
	removalSeq := e.tailscaleRemovalSeq
	if e.authConns == nil {
		e.authConns = make(map[*tls.Conn]string)
	}
	e.authConns[conn] = expectedID
	if route == lan.RouteTailscale {
		if e.tailscaleConns == nil {
			e.tailscaleConns = make(map[*tls.Conn]struct{})
		}
		e.tailscaleConns[conn] = struct{}{}
	}
	e.authMu.Unlock()
	defer func() {
		e.authMu.Lock()
		delete(e.authConns, conn)
		delete(e.tailscaleConns, conn)
		e.authMu.Unlock()
	}()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := conn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("TLS 握手失败：%w", err)
	}
	peer, hello, err := e.handshake(conn)
	if err != nil {
		return err
	}
	if expectedID != "" && peer.id != expectedID {
		return ErrWrongPeer
	}
	e.authMu.Lock()
	e.authConns[conn] = peer.id
	if route == lan.RouteTailscale && e.tailscaleRemovals[peer.id] > removalSeq {
		e.authMu.Unlock()
		return ErrTailscaleAddressRemoved
	}
	e.authMu.Unlock()
	paired, err := e.authorizePair(conn, peer, hello, epoch, weDialed, sequence)
	if err != nil {
		if errors.Is(err, ErrFingerprintMismatch) {
			e.notify(Notice{Kind: NoticeError, Message: "设备身份发生变化，已拒绝连接。确认对端重装后，解除配对再重新添加", PeerID: peer.id, DialSeq: sequence})
		} else {
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = wire.WriteMsg(conn, wire.TypeError, wire.Error{Code: 2, Msg: err.Error()})
			e.notify(Notice{Kind: NoticeError, Message: err.Error(), PeerID: peer.id, DialSeq: sequence})
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
	sess.admit = e.admitClipboard
	sess.fileCache = e.FileCache
	sess.clipboardSequence = e.Board.Sequence
	sess.captureFileClipboard = func(seq, version uint64) (func(clip.Item) error, []string, error) {
		return e.captureFileClipboard(sess, seq, version)
	}
	sess.fileClipboardCheck = func(item queuedItem) error { return e.checkFileClipboard(sess, item) }
	sess.onItem = func(item clip.Item) error { return e.deliverFrom(sess, item) }
	sess.onClipboardResult = func(result wire.Ack) {
		if result.State != "complete" && result.State != "sending" {
			e.allowLocalRetry(sess, result.ItemID)
		}
		if result.State != "sending" {
			e.Log.Info("对端剪贴板处理结果", "item", result.ItemID, "state", result.State, "message", result.Message)
		}
		e.notify(Notice{Kind: NoticeSync, Message: result.Message, PeerID: peer.id, PeerName: hello.Name, ClipboardState: result.State, SessionID: sess.sessionID})
	}
	for _, cap := range hello.Capabilities {
		if cap == wire.FileCapability {
			sess.fileSupported = true
		}
		if cap == wire.FileClipboardCapability {
			sess.fileClipboardSupported = true
		}
		if cap == wire.ClipboardResultCapability {
			sess.clipboardResultSupported = true
		}
	}
	sess.receiveDir = e.ReceiveDir
	sess.fileLimit = e.FileLimit
	sess.onFile = func(n Notice) { n.PeerID = peer.id; n.PeerName = hello.Name; e.notify(n) }
	e.authMu.Lock()
	if epoch != e.authEpoch || ctx.Err() != nil {
		e.authMu.Unlock()
		return ErrPairingStopped
	}
	if route == lan.RouteTailscale && (!e.allowTailscale || networkEpoch != e.tailscaleEpoch) {
		e.authMu.Unlock()
		return lan.ErrTailscaleDisabled
	}
	if route == lan.RouteTailscale && e.tailscaleRemovals[peer.id] > removalSeq {
		e.authMu.Unlock()
		return ErrTailscaleAddressRemoved
	}
	_, err = e.Peers.AllowEndpoint(peer.id, peer.fp, hello.Name, hello.Platform, lan.Endpoint{Addr: reconnectAddr(addr, weDialed, hello.ListenPort), Route: route})
	if err != nil {
		e.authMu.Unlock()
		e.notify(Notice{Kind: NoticeError, Message: "保存配对失败，请检查数据目录权限后重试", PeerID: peer.id, DialSeq: sequence})
		return err
	}
	if paired {
		e.pairingUntil = time.Time{}
	}
	adopted := e.adopt(sess, Link{SessionID: sess.sessionID, ID: peer.id, Name: hello.Name, Platform: hello.Platform, Addr: addr, FileTransfer: sess.fileSupported, Route: route})
	e.authMu.Unlock()
	if !adopted {
		e.Log.Info("关掉重复连接", "peer", peer.id, "dialed", weDialed)
		return nil
	}
	e.Log.Info("已连接", "peer", peer.id, "name", hello.Name, "platform", hello.Platform, "dialed", weDialed)
	e.notify(Notice{
		Kind:      NoticeStatus,
		Message:   "已通过" + route.Label() + "连接 " + hello.Name,
		PeerID:    peer.id,
		PeerName:  hello.Name,
		Platform:  hello.Platform,
		Addr:      addr,
		Route:     route,
		SessionID: sess.sessionID,
		DialSeq:   sequence,
	})
	sess.run()
	if e.drop(sess) {
		e.Log.Info("连接断开", "peer", peer.id)
		e.notify(Notice{Kind: NoticeStatus, Message: "设备离线，等待重连", PeerID: peer.id, PeerName: hello.Name, SessionID: sess.sessionID, DialSeq: sequence})
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
		Capabilities: []string{wire.FileCapability, wire.PairCapability, wire.ClipboardResultCapability, wire.FileClipboardCapability},
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
		e.haveLast = false
		e.lastSendID = ""
		return true
	}
	newWins := keep(e.LocalID, s.peerID, s.weDialed) && !keep(e.LocalID, s.peerID, cur.weDialed)
	if newWins {
		cur.Close()
		e.sess = s
		e.link = link
		e.haveLast = false
		e.lastSendID = ""
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
