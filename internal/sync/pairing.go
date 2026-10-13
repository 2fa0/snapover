package sync

import (
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/2fa0/snapover/internal/wire"
)

const (
	PairingWindow = 2 * time.Minute
	pairWait      = 90 * time.Second
	pairCooldown  = 10 * time.Second
)

var ErrPairingStopped = errors.New("配对已结束，请在两台电脑上点击「添加设备」后重试")
var ErrWrongPeer = errors.New("地址上的设备身份不符，请重新选择设备")

// PendingPair is tied to one TLS connection; stale buttons cannot approve another.
type PendingPair struct {
	Token       uint64
	PeerID      string
	Name        string
	Platform    string
	Fingerprint string
	Code        string
	Expires     time.Time
	Confirmed   bool
}

type pairRequest struct {
	PendingPair
	conn   *tls.Conn
	choice chan bool
	done   chan struct{}
}

// stop is called with authMu held, including when the peer has already sent
// its choice and the request is only waiting for the local user.
func (p *pairRequest) stop() {
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	_ = p.conn.NetConn().Close()
}

func (e *Engine) SetPairing(on bool) {
	e.authMu.Lock()
	if on {
		e.pairingUntil = time.Now().Add(PairingWindow)
	} else {
		e.pairingUntil = time.Time{}
		e.authEpoch++
		if e.pendingPair != nil {
			e.pendingPair.stop()
		}
	}
	e.authMu.Unlock()
}

func (e *Engine) PairingUntil() time.Time {
	e.authMu.Lock()
	defer e.authMu.Unlock()
	return e.pairingUntil
}

func (e *Engine) PendingPair() (PendingPair, bool) {
	e.authMu.Lock()
	defer e.authMu.Unlock()
	if e.pendingPair == nil {
		return PendingPair{}, false
	}
	return e.pendingPair.PendingPair, true
}

func (e *Engine) ConfirmPair(token uint64, accept bool) bool {
	e.authMu.Lock()
	defer e.authMu.Unlock()
	p := e.pendingPair
	if p == nil || p.Token != token || p.Confirmed || !time.Now().Before(p.Expires) {
		return false
	}
	p.Confirmed = true
	p.choice <- accept
	return true
}

// Revoke serializes trust removal with session adoption, so an in-flight
// handshake cannot restore trust after the user removes a device.
func (e *Engine) Revoke(id string) error {
	e.authMu.Lock()
	defer e.authMu.Unlock()
	var err error
	if id == "" {
		err = e.Peers.Reset()
	} else {
		err = e.Peers.Forget(id)
	}
	if err != nil {
		return err
	}
	e.authEpoch++
	e.pairingUntil = time.Time{}
	if e.pendingPair != nil {
		e.pendingPair.stop()
	}
	for conn, peer := range e.authConns {
		if id == "" || peer == id {
			_ = conn.NetConn().Close()
		}
	}
	// Clipboard writes can wait for the UI thread; UI revocation must not
	// wait on clipMu. deliverFrom validates the session on the clipboard thread.
	e.mu.Lock()
	if e.sess != nil && (id == "" || e.sess.peerID == id) {
		e.sess.Close()
		e.sess = nil
		e.link = Link{}
	}
	e.mu.Unlock()
	return nil
}

func (e *Engine) Disconnect() {
	e.mu.Lock()
	sess := e.sess
	e.sess = nil
	e.link = Link{}
	e.mu.Unlock()
	if sess != nil {
		_ = sess.conn.NetConn().Close()
		sess.Close()
	}
}

func (e *Engine) Stop() {
	e.authMu.Lock()
	e.authEpoch++
	e.pairingUntil = time.Time{}
	if e.pendingPair != nil {
		e.pendingPair.stop()
	}
	for conn := range e.authConns {
		_ = conn.NetConn().Close()
	}
	e.authMu.Unlock()
	e.Disconnect()
}

func exchangePair(conn *tls.Conn, typ byte, send, recv any) error {
	written := make(chan error, 1)
	go func() { written <- wire.WriteMsg(conn, typ, send) }()
	got, payload, err := wire.ReadFrame(conn)
	if err != nil {
		_ = conn.NetConn().Close()
	}
	werr := <-written
	if err != nil {
		return err
	}
	if werr != nil {
		return werr
	}
	if got == wire.TypeError {
		var msg wire.Error
		_ = wire.Unmarshal(payload, &msg)
		return fmt.Errorf("%w：%s", ErrPairingStopped, msg.Msg)
	}
	if got != typ {
		return fmt.Errorf("%w：对端未完成配对握手", ErrPairingStopped)
	}
	return wire.Unmarshal(payload, recv)
}

func pairingCode(conn *tls.Conn) (string, error) {
	state := conn.ConnectionState()
	// TLS exporters bind the code to this mutual-TLS handshake, including
	// both certificates. A fresh connection has a different code.
	key, err := state.ExportKeyingMaterial("EXPORTER-Snapover-Pair-v1", nil, 32)
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%06d", binary.BigEndian.Uint32(key[:4])%1000000)
	return code[:3] + " " + code[3:], nil
}

func (e *Engine) authorizePair(conn *tls.Conn, peer *identityResult, hello wire.Hello, epoch uint64, weDialed bool) (bool, error) {
	trusted, err := e.Peers.Check(peer.id, peer.fp)
	if err != nil {
		return false, err
	}
	modern := false
	for _, cap := range hello.Capabilities {
		modern = modern || cap == wire.PairCapability
	}
	if !modern {
		if trusted {
			return false, nil
		}
		return false, fmt.Errorf("%w：首次配对需要两台电脑都升级 Snapover", ErrPairingStopped)
	}
	var remote wire.PairState
	if err := exchangePair(conn, wire.TypePairState, wire.PairState{Trusted: trusted}, &remote); err != nil {
		return false, err
	}
	if trusted && remote.Trusted {
		return false, nil
	}
	code, err := pairingCode(conn)
	if err != nil {
		return false, err
	}
	now := time.Now()
	e.authMu.Lock()
	preferred := keep(e.LocalID, peer.id, weDialed)
	promotion := e.pairLastID == peer.id && preferred && !e.pairPreferred
	// A preferred connection gets a new token and requires fresh confirmation,
	// even if one side already approved the old connection. Refusing promotion
	// on only that side would leave the two peers using different connections.
	busy := e.pendingPair != nil && (e.pendingPair.PeerID != peer.id || !promotion)
	if epoch != e.authEpoch || !now.Before(e.pairingUntil) || busy || (now.Sub(e.pairLast) < pairCooldown && !promotion) {
		e.authMu.Unlock()
		return false, ErrPairingStopped
	}
	if e.pendingPair != nil {
		e.pendingPair.stop()
	}
	e.pairLast = now
	e.pairLastID = peer.id
	e.pairPreferred = preferred
	e.pairSerial++
	p := &pairRequest{
		PendingPair: PendingPair{Token: e.pairSerial, PeerID: peer.id, Name: hello.Name, Platform: hello.Platform, Fingerprint: peer.fp, Code: code, Expires: now.Add(pairWait)},
		conn:        conn, choice: make(chan bool, 1), done: make(chan struct{}),
	}
	e.pendingPair = p
	e.authMu.Unlock()
	defer func() {
		e.authMu.Lock()
		if e.pendingPair == p {
			e.pendingPair = nil
		}
		e.authMu.Unlock()
		e.notify(Notice{Kind: NoticePairing, Message: ""})
	}()
	_ = conn.SetDeadline(p.Expires)
	e.notify(Notice{Kind: NoticePairing, Message: "核对两台电脑的校验码后确认配对", PeerID: peer.id, PeerName: hello.Name})
	// Read immediately so cancellation or rejection on the other computer
	// ends the prompt even when the local user has not clicked anything.
	remoteChoice := make(chan error, 1)
	go func() {
		typ, payload, err := wire.ReadFrame(conn)
		var choice wire.PairChoice
		if err == nil && typ == wire.TypePairChoice {
			err = wire.Unmarshal(payload, &choice)
		}
		if err == nil && (typ != wire.TypePairChoice || !choice.Accept) {
			err = ErrPairingStopped
		}
		remoteChoice <- err
	}()
	timer := time.NewTimer(time.Until(p.Expires))
	defer timer.Stop()
	remoteAccepted := false
	for {
		select {
		case <-p.done:
			return false, ErrPairingStopped
		case err := <-remoteChoice:
			if err != nil {
				return false, fmt.Errorf("%w：对端拒绝、取消或连接断开", ErrPairingStopped)
			}
			remoteAccepted = true
			remoteChoice = nil
		case accept := <-p.choice:
			if err := wire.WriteMsg(conn, wire.TypePairChoice, wire.PairChoice{Accept: accept}); err != nil {
				return false, err
			}
			if !accept {
				return false, ErrPairingStopped
			}
			e.notify(Notice{Kind: NoticePairing, Message: "已确认，等待另一台电脑确认", PeerID: peer.id, PeerName: hello.Name})
			if !remoteAccepted {
				select {
				case <-p.done:
					return false, ErrPairingStopped
				case err := <-remoteChoice:
					if err != nil {
						return false, fmt.Errorf("%w：对端拒绝、取消或连接断开", ErrPairingStopped)
					}
				case <-timer.C:
					return false, fmt.Errorf("%w：确认超时", ErrPairingStopped)
				}
			}
			return true, nil
		case <-timer.C:
			return false, fmt.Errorf("%w：确认超时", ErrPairingStopped)
		}
	}
}
