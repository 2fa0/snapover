package sync

import (
	"context"
	"errors"

	"github.com/2fa0/snapover/internal/lan"
)

var ErrTailscaleAddressRemoved = errors.New("该设备的 Tailscale 地址已删除，请重新连接")

type tailscaleDial struct {
	peerID string
	cancel context.CancelFunc
}

// CancelDial orders an explicit cancellation before any later adoption or save.
func (e *Engine) CancelDial(cancel context.CancelFunc) {
	e.authMu.Lock()
	defer e.authMu.Unlock()
	cancel()
}

func (e *Engine) tailscaleContext(parent context.Context, peerID string) (context.Context, func(), error) {
	e.authMu.Lock()
	defer e.authMu.Unlock()
	if !e.allowTailscale {
		return nil, nil, lan.ErrTailscaleDisabled
	}
	ctx, cancel := context.WithCancel(parent)
	e.nextNetworkDial++
	id := e.nextNetworkDial
	if e.tailscaleDials == nil {
		e.tailscaleDials = make(map[uint64]tailscaleDial)
	}
	e.tailscaleDials[id] = tailscaleDial{peerID: peerID, cancel: cancel}
	return ctx, func() {
		cancel()
		e.authMu.Lock()
		delete(e.tailscaleDials, id)
		e.authMu.Unlock()
	}, nil
}

func (e *Engine) TailscaleEnabled() bool {
	e.authMu.Lock()
	defer e.authMu.Unlock()
	return e.allowTailscale
}

// SetAllowTailscale serializes a policy change with trust persistence and adoption.
// Closing a tunnel during a handshake cannot make it trusted after disabling it.
func (e *Engine) SetAllowTailscale(enabled bool) {
	e.authMu.Lock()
	defer e.authMu.Unlock()
	if e.allowTailscale == enabled {
		return
	}
	e.allowTailscale = enabled
	if enabled {
		return
	}
	e.tailscaleEpoch++
	for _, dial := range e.tailscaleDials {
		dial.cancel()
	}
	for conn := range e.tailscaleConns {
		_ = conn.NetConn().Close()
	}
	if e.pendingPair != nil {
		if _, ok := e.tailscaleConns[e.pendingPair.conn]; ok {
			e.pendingPair.stop()
		}
	}
	e.mu.Lock()
	if e.sess != nil && e.link.Route == lan.RouteTailscale {
		e.sess.Close()
		e.sess = nil
		e.link = Link{}
	}
	e.mu.Unlock()
}

// RemoveTailscaleAddress cancels older edits before they can restore an address.
func (e *Engine) RemoveTailscaleAddress(id string) error {
	e.authMu.Lock()
	defer e.authMu.Unlock()
	if err := e.Peers.RemoveTailscaleAddress(id); err != nil {
		return err
	}
	// Unknown incoming connections are checked against this device's removal
	// after Hello, instead of canceling unrelated, not-yet-identified peers.
	e.tailscaleRemovalSeq++
	if e.tailscaleRemovals == nil {
		e.tailscaleRemovals = make(map[string]uint64)
	}
	e.tailscaleRemovals[id] = e.tailscaleRemovalSeq
	for _, dial := range e.tailscaleDials {
		if dial.peerID == id {
			dial.cancel()
		}
	}
	for conn := range e.tailscaleConns {
		if peer := e.authConns[conn]; peer == id {
			_ = conn.NetConn().Close()
		}
	}
	e.mu.Lock()
	if e.sess != nil && e.link.ID == id && e.link.Route == lan.RouteTailscale {
		e.sess.Close()
		e.sess = nil
		e.link = Link{}
	}
	e.mu.Unlock()
	return nil
}
