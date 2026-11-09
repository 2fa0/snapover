package sync

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
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
		route, routeErr := lan.IncomingRoute(tlsConn, e.TailscaleEnabled())
		if routeErr != nil {
			e.Log.Info("忽略不允许的连接", "local", tlsConn.LocalAddr().String(), "remote", tlsConn.RemoteAddr().String(), "err", routeErr)
			_ = tlsConn.Close()
			continue
		}
		enableKeepAlive(tlsConn)
		go func() {
			if err := e.attachPeer(ctx, tlsConn, false, "", route); err != nil {
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
	Reconnect(ctx, NewDialTarget(expectedID, 0, []lan.Endpoint{{Addr: addr, Route: lan.RouteLAN}}), e)
}

// DialCandidate is one reconnect path and the identity that path must present.
// Discovery and saved addresses are routing hints, never proof of identity.
type DialCandidate struct {
	PeerID   string
	Endpoint lan.Endpoint
}

// DialTarget keeps reconnect paths while discovery updates routing hints.
// An explicit retry pins one identity, at most three paths.
// Automatic reconnect may include other already-trusted devices, still at most
// three paths each, and still handshakes against that candidate's id.
type DialTarget struct {
	peerID     string
	Sequence   uint64
	mu         sync.Mutex
	candidates []DialCandidate
	preferred  lan.Endpoint
}

func NewDialTarget(peerID string, sequence uint64, endpoints []lan.Endpoint) *DialTarget {
	t := &DialTarget{peerID: peerID, Sequence: sequence}
	t.Update(endpoints)
	return t
}

func (t *DialTarget) PeerID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.peerID
}

// BindPeer fixes the identity after a manually addressed first pairing succeeds.
// It cannot retarget an already identified device.
func (t *DialTarget) BindPeer(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.peerID == "" {
		t.peerID = id
	}
}

func (t *DialTarget) Prefer(ep lan.Endpoint) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.preferred = ep
}

func (t *DialTarget) firstCandidates() []DialCandidate {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.preferred.Addr == "" {
		return slices.Clone(t.candidates)
	}
	out := []DialCandidate{{PeerID: t.peerID, Endpoint: t.preferred}}
	for _, c := range t.candidates {
		if !containsCandidate(out, c) {
			out = append(out, c)
		}
	}
	return out
}

func containsCandidate(list []DialCandidate, c DialCandidate) bool {
	for _, have := range list {
		if have.Endpoint != c.Endpoint {
			continue
		}
		if have.PeerID == c.PeerID || have.PeerID == "" || c.PeerID == "" {
			return true
		}
	}
	return false
}

func (t *DialTarget) Update(endpoints []lan.Endpoint) {
	id := t.PeerID()
	cands := make([]DialCandidate, 0, len(endpoints))
	for _, ep := range endpoints {
		cands = append(cands, DialCandidate{PeerID: id, Endpoint: ep})
	}
	t.UpdateCandidates(cands)
}

func (t *DialTarget) UpdateCandidates(candidates []DialCandidate) {
	t.mu.Lock()
	defer t.mu.Unlock()
	perPeer := map[string]int{}
	next := make([]DialCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c.Endpoint.Addr == "" {
			continue
		}
		id := c.PeerID
		if id == "" {
			id = t.peerID
		}
		c.PeerID = id
		if containsCandidate(next, c) {
			continue
		}
		if perPeer[id] >= 3 {
			continue
		}
		perPeer[id]++
		next = append(next, c)
	}
	t.candidates = next
}

func (t *DialTarget) Endpoints() []lan.Endpoint {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]lan.Endpoint, len(t.candidates))
	for i, c := range t.candidates {
		out[i] = c.Endpoint
	}
	return out
}

func (t *DialTarget) Candidates() []DialCandidate {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.candidates)
}

func (t *DialTarget) hasTrusted(e *Engine) bool {
	if e == nil || e.Peers == nil {
		return false
	}
	t.mu.Lock()
	cands := slices.Clone(t.candidates)
	preferred := t.peerID
	t.mu.Unlock()
	if preferred != "" {
		if _, ok := e.Peers.Get(preferred); ok {
			return true
		}
	}
	for _, c := range cands {
		id := c.PeerID
		if id == "" {
			id = preferred
		}
		if id == "" {
			continue
		}
		if _, ok := e.Peers.Get(id); ok {
			return true
		}
	}
	return false
}

func Reconnect(ctx context.Context, target *DialTarget, e *Engine) {
	reconnect(ctx, target, e, dialEndpoint, wait)
}

type dialSequenceKey struct{}

func dialSequence(ctx context.Context) uint64 {
	sequence, _ := ctx.Value(dialSequenceKey{}).(uint64)
	return sequence
}

// The attempt and pause parameters keep routing failures and backoff testable
// without touching a real VPN, clipboard, or user identity directory.
func reconnect(ctx context.Context, target *DialTarget, e *Engine,
	attempt func(context.Context, lan.Endpoint, string, *Engine) error,
	pause func(context.Context, time.Duration) bool) {
	ctx = context.WithValue(ctx, dialSequenceKey{}, target.Sequence)
	backoff := time.Second
	blocked := map[lan.Endpoint]bool{}
	first := true
	initialID := target.PeerID()
	_, wasTrusted := e.Peers.Get(initialID)
	for ctx.Err() == nil {
		peerID := target.PeerID()
		_, trusted := e.Peers.Get(peerID)
		if !trusted && (wasTrusted || (peerID != "" && peerID != initialID)) && !target.hasTrusted(e) {
			return
		}
		wasTrusted = wasTrusted || trusted || target.hasTrusted(e)
		if e.Connected() {
			backoff = time.Second
			if !pause(ctx, 500*time.Millisecond) {
				return
			}
			continue
		}
		candidates := target.Candidates()
		if first {
			candidates = target.firstCandidates()
			first = false
		}
		for ep := range blocked {
			keep := false
			for _, c := range candidates {
				if c.Endpoint == ep {
					keep = true
					break
				}
			}
			if !keep {
				delete(blocked, ep)
			}
		}
		for _, cand := range candidates {
			if ctx.Err() != nil || e.Connected() {
				break
			}
			ep := cand.Endpoint
			id := cand.PeerID
			if id == "" {
				id = peerID
			}
			if id != "" {
				if _, ok := e.Peers.Get(id); !ok && wasTrusted {
					continue
				}
			}
			if blocked[ep] {
				continue
			}
			e.notify(Notice{Kind: NoticeDial, Message: "正在通过" + ep.Route.Label() + "连接 " + ep.Addr,
				PeerID: id, Addr: ep.Addr, Route: ep.Route, DialSeq: target.Sequence})
			err := attempt(ctx, ep, id, e)
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				backoff = time.Second
				break
			}
			e.notify(Notice{Kind: NoticeDial, Message: err.Error(), PeerID: id,
				Addr: ep.Addr, Route: ep.Route, DialSeq: target.Sequence})
			if errors.Is(err, ErrPairingStopped) {
				return
			}
			if errors.Is(err, ErrWrongPeer) || errors.Is(err, ErrFingerprintMismatch) {
				blocked[ep] = true
				if id == "" {
					return
				}
			}
		}
		if !pause(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func dialEndpoint(ctx context.Context, ep lan.Endpoint, expectedID string, e *Engine) error {
	if ep.Route == lan.RouteTailscale {
		networkCtx, finish, err := e.tailscaleContext(ctx, expectedID)
		if err != nil {
			return err
		}
		defer finish()
		ctx = networkCtx
	}
	conn, bind, err := lan.DialContext(ctx, ep, e.TailscaleEnabled(), 5*time.Second)
	if err != nil {
		if e.Log != nil {
			e.Log.Warn("拨号失败", "addr", ep.Addr, "route", ep.Route, "bind", bindString(bind), "err", err)
		}
		return fmt.Errorf("%s连接失败（%s）：%w", ep.Route.Label(), ep.Addr, err)
	}
	if e.Log != nil {
		e.Log.Info("正在拨号", "addr", ep.Addr, "route", ep.Route, "bind", bindString(bind))
	}
	enableKeepAlive(conn)
	err = e.attachPeer(ctx, tls.Client(conn, e.TLS), true, expectedID, ep.Route)
	if ep.Route == lan.RouteTailscale && !e.TailscaleEnabled() {
		return lan.ErrTailscaleDisabled
	}
	return err
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
