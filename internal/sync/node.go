package sync

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"

	"github.com/2fa0/snapover/internal/lan"
)

// Listen accepts peers until ctx is cancelled.
func Listen(ctx context.Context, addr string, e *Engine) error {
	ln, err := tls.Listen("tcp", addr, e.TLS)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		e.Stop()
	}()
	e.Log.Info("正在监听", "addr", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			e.Log.Warn("接受连接失败", "err", err)
			continue
		}
		tlsConn, ok := conn.(*tls.Conn)
		if !ok {
			_ = conn.Close()
			continue
		}
		if !lan.AllowLocal(lan.LocalIP(tlsConn)) {
			e.Log.Info("忽略虚拟网卡上的连接", "local", tlsConn.LocalAddr().String(), "remote", tlsConn.RemoteAddr().String())
			_ = tlsConn.Close()
			continue
		}
		enableKeepAlive(tlsConn)
		go func() {
			if err := e.Attach(tlsConn, false); err != nil {
				e.Log.Warn("接入失败", "addr", tlsConn.RemoteAddr().String(), "err", err)
			}
		}()
	}
}

// Dial keeps a connection to addr until ctx is cancelled.
// It does not open a second connection while one is already up.
func Dial(ctx context.Context, addr string, e *Engine) {
	DialPeer(ctx, addr, "", e)
}

func DialPeer(ctx context.Context, addr, expectedID string, e *Engine) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if e.Connected() {
			if !wait(ctx, 500*time.Millisecond) {
				return
			}
			continue
		}
		conn, bind, err := lan.DialTimeout(addr, 5*time.Second)
		if err == nil && bind != nil {
			e.Log.Info("从局域网网卡拨号", "addr", addr, "bind", bind.String())
		}
		if err != nil {
			msg := "连不上 " + addr
			if bind != nil {
				msg += "（已从 " + bind.String() + " 拨出，不走虚拟网卡）"
			}
			e.Log.Warn("拨号失败", "addr", addr, "bind", bindString(bind), "err", err)
			e.notify(Notice{Kind: NoticeError, Message: msg, Addr: addr})
			if !wait(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		enableKeepAlive(conn)
		tlsConn := tls.Client(conn, e.TLS)
		_ = tlsConn.SetDeadline(time.Now().Add(10 * time.Second))
		err = tlsConn.Handshake()
		_ = tlsConn.SetDeadline(time.Time{})
		if err != nil {
			_ = conn.Close()
			e.Log.Warn("TLS 握手失败", "addr", addr, "err", err)
			e.notify(Notice{Kind: NoticeError, Message: "TLS 握手失败", Addr: addr})
			if !wait(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		attached := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-attached:
			}
		}()
		err = e.AttachPeer(tlsConn, true, expectedID)
		close(attached)
		if errors.Is(err, ErrPairingStopped) || errors.Is(err, ErrFingerprintMismatch) || errors.Is(err, ErrWrongPeer) {
			e.notify(Notice{Kind: NoticeError, Message: err.Error(), PeerID: expectedID, Addr: addr})
			return
		}
		if ctx.Err() != nil {
			return
		}
		if e.Connected() {
			// This dial lost the tie-break. The other session is still up.
			backoff = time.Second
			continue
		}
		if err != nil {
			e.Log.Warn("连接失败", "addr", addr, "err", err)
		} else {
			e.Log.Info("连接结束，准备重连", "addr", addr)
		}
		if !wait(ctx, backoff) {
			return
		}
		if err != nil {
			backoff = min(backoff*2, 30*time.Second)
		} else {
			backoff = time.Second
		}
	}
}

func enableKeepAlive(conn net.Conn) {
	raw := conn
	if tlsConn, ok := conn.(*tls.Conn); ok {
		raw = tlsConn.NetConn()
	}
	tcp, ok := raw.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tcp.SetKeepAlive(true)
	_ = tcp.SetKeepAlivePeriod(15 * time.Second)
}

func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func bindString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}
