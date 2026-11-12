package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/2fa0/snapover/internal/lan"
	clipsync "github.com/2fa0/snapover/internal/sync"
	"github.com/egoist/mygo/ui"
)

func (a *app) endpointForAddress(addr string) lan.Endpoint {
	for _, dev := range a.devices {
		if dev.Peer.HasAddress(addr) {
			return dev.Peer.Endpoint(addr)
		}
	}
	return lan.Endpoint{Addr: addr, Route: lan.RouteLAN}
}

func (a *app) peerIDForAddress(addr string) string {
	for _, dev := range a.devices {
		if dev.Peer.HasAddress(addr) {
			return dev.ID
		}
	}
	return ""
}

func (a *app) connectionCandidates() []clipsync.DialCandidate {
	if a.dialPinned {
		return a.candidatesFor(a.targetPeerID)
	}
	var out []clipsync.DialCandidate
	seen := map[string]bool{}
	for _, id := range a.autoPeerOrder() {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, a.candidatesFor(id)...)
	}
	return out
}

func (a *app) autoPeerOrder() []string {
	var ids []string
	if a.targetPeerID != "" {
		ids = append(ids, a.targetPeerID)
	}
	if a.engine != nil && a.engine.Peers != nil {
		for _, dev := range a.engine.Peers.List() {
			ids = append(ids, dev.ID)
		}
	}
	return ids
}

func (a *app) candidatesFor(peerID string) []clipsync.DialCandidate {
	tag := func(ep lan.Endpoint) clipsync.DialCandidate {
		return clipsync.DialCandidate{PeerID: peerID, Endpoint: ep}
	}
	var p clipsync.Peer
	paired := false
	if a.engine != nil && a.engine.Peers != nil {
		p, paired = a.engine.Peers.Get(peerID)
	}
	if !paired {
		if a.dialProposed.Addr == "" || (a.dialProposed.Route == lan.RouteTailscale && !a.allowTailscale) {
			return nil
		}
		if peerID != "" && peerID != a.targetPeerID {
			return nil
		}
		return []clipsync.DialCandidate{tag(a.dialProposed)}
	}
	if peerID == a.targetPeerID {
		// Proposed edits stay in memory until a handshake verifies this device.
		if ep := a.dialProposed; ep.Addr != "" {
			// Resolve the legacy address before overriding a classified endpoint.
			p.LANAddr = p.LANAddress()
			p.Addr = ""
			if ep.Route == lan.RouteTailscale {
				p.TailscaleAddr = ep.Addr
			} else {
				p.LANAddr = ep.Addr
			}
		}
	}
	var out []clipsync.DialCandidate
	for _, found := range a.discovered {
		if found.ID == peerID {
			out = append(out, tag(lan.Endpoint{Addr: found.Addr, Route: lan.RouteLAN}))
			break
		}
	}
	for _, ep := range p.Endpoints(a.allowTailscale) {
		out = append(out, tag(ep))
	}
	return out
}

func (a *app) refreshConnectionCandidates() {
	if a.dialTarget != nil {
		a.dialTarget.UpdateCandidates(a.connectionCandidates())
	}
}

func (a *app) connect() {
	addr, err := normalizePeer(a.peer)
	if err != nil || addr == "" {
		a.reportStatusError("请填写对端地址，端口可省略")
		return
	}
	explicit := a.startupPeerExplicit
	a.startupPeerExplicit = false
	a.startConnection(a.endpointForAddress(addr), a.targetPeerID, explicit)
}

func (a *app) connectAddress() {
	addr, err := normalizePeer(a.peer)
	if err != nil || addr == "" {
		a.reportStatusError("请填写对端地址，端口可省略")
		return
	}
	route := lan.RouteLAN
	if a.addressRoute == 1 {
		route = lan.RouteTailscale
	}
	a.startConnection(lan.Endpoint{Addr: addr, Route: route}, a.peerIDForAddress(addr), true)
}

func (a *app) startConnection(ep lan.Endpoint, peerID string, explicit bool) {
	if err := ep.Validate(); err != nil {
		a.reportStatusError(err.Error())
		return
	}
	if ep.Route == lan.RouteTailscale {
		if !a.allowTailscale {
			a.reportStatusError(lan.ErrTailscaleDisabled.Error())
			return
		}
		if err := lan.CheckTailscaleListener(a.listen); err != nil {
			a.reportStatusError(err.Error())
			return
		}
	}
	if peerID == "" {
		peerID = a.peerIDForAddress(ep.Addr)
	}
	a.cancelProbe()
	a.stopDial()
	a.dialPinned = explicit
	a.peer, a.targetPeerID = ep.Addr, peerID
	a.addressRoute = 0
	if ep.Route == lan.RouteTailscale {
		a.addressRoute = 1
	}
	if explicit || !a.isPaired(peerID) {
		a.dialProposed = ep
	}
	parent := a.root
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	a.dialCancel, a.dialAddr = cancel, ep.Addr
	if link, up := a.engine.Link(); up && (link.ID != peerID || explicit) {
		a.engine.Disconnect()
	}
	if !a.isPaired(peerID) {
		a.engine.SetPairing(true)
	}
	target := clipsync.NewDialTarget(peerID, a.dialSeq, nil)
	target.UpdateCandidates(a.connectionCandidates())
	if explicit {
		target.Prefer(ep)
	}
	a.dialTarget = target
	a.status = "正在通过" + ep.Route.Label() + "连接 " + ep.Addr
	seq := a.dialSeq
	go func() {
		clipsync.Reconnect(ctx, target, a.engine)
		if ctx.Err() == nil {
			a.update(func() {
				if a.dialSeq == seq {
					a.dialCancel = nil
				}
			})
		}
	}()
}

func (a *app) tailscaleSettings(c *ui.Context) {
	if settingSwitch(c, &a.allowTailscale, a.tr("允许通过 Tailscale 连接"), "") {
		a.saveTailscaleSetting()
	}
	if a.tailscaleError != "" {
		ui.Text(c, a.tr(a.tailscaleError)).FontSize(12).TextColor(c.Theme().Danger)
	}
}

func (a *app) saveTailscaleSetting() {
	previous := a.cfg.Values.AllowTailscale
	if a.allowTailscale {
		if err := lan.CheckTailscaleListener(a.listen); err != nil {
			a.allowTailscale = previous
			a.tailscaleError = err.Error()
			return
		}
	}
	a.cfg.Values.AllowTailscale = a.allowTailscale
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values.AllowTailscale, a.allowTailscale = previous, previous
		a.tailscaleError = "保存 Tailscale 设置失败，设置未生效，请检查数据目录权限后重试"
		return
	}
	a.cancelProbe()
	a.engine.SetAllowTailscale(a.allowTailscale)
	a.tailscaleError = ""
	if !a.allowTailscale && a.dialProposed.Route == lan.RouteTailscale {
		a.dialProposed = lan.Endpoint{}
	}
	a.refreshConnectionCandidates()
	if a.dialCancel == nil {
		if p, ok := a.engine.Peers.Get(a.targetPeerID); ok {
			if endpoints := p.Endpoints(a.allowTailscale); len(endpoints) > 0 {
				a.startConnection(endpoints[0], a.targetPeerID, false)
			}
		}
	}
	if a.allowTailscale {
		a.status = "已允许 Tailscale 连接，可在连接设备页添加地址"
	} else {
		a.status = "已关闭 Tailscale 连接，已保存的地址仍保留"
	}
}

func (a *app) tailscaleDeviceAddress(c *ui.Context, dev clipsync.Device, waiting bool) {
	if addr := dev.Peer.LANAddress(); addr != "" {
		kv(c, a.tr("局域网地址"), addr)
	}
	if addr := dev.Peer.TailscaleAddr; addr != "" {
		kv(c, a.tr("Tailscale 地址"), addr)
	}
	if link, up := a.engine.Link(); up && link.ID == dev.ID {
		kv(c, a.tr("当前连接"), a.tr(link.Route.Label()))
	}
	if a.editTailscaleID == dev.ID {
		ui.Text(c, a.tr("填写该设备的 Tailscale IPv4，验证设备身份后保存。")).FontSize(12).TextColor(c.Theme().TextMuted)
		submit := ui.TextInput(c.Key("tailscale-address"), &a.editTailscaleAddr).Label(a.tr("Tailscale IPv4 地址")).Placeholder(a.tr("100.x.y.z，端口可省略")).Submitted()
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, a.tr("验证并连接")).Disabled(waiting || !a.allowTailscale).Clicked() || (submit && !waiting && a.allowTailscale) {
				a.connectTailscaleAddress(dev.ID)
			}
			if ui.Button(c, a.tr("取消编辑")).Clicked() {
				a.editTailscaleID, a.editTailscaleAddr = "", ""
			}
		})
	} else {
		ui.Row(c).Gap(8).Children(func() {
			label := a.tr("添加 Tailscale 地址")
			if dev.Peer.TailscaleAddr != "" {
				label = a.tr("修改 Tailscale 地址")
			}
			if ui.Button(c, label).Disabled(waiting || !a.allowTailscale).Clicked() {
				a.editTailscaleID, a.editTailscaleAddr = dev.ID, dev.Peer.TailscaleAddr
			}
			if dev.Peer.TailscaleAddr != "" && ui.Button(c, a.tr("删除 Tailscale 地址")).Disabled(waiting).Clicked() {
				a.removeTailscaleAddress(dev.ID)
			}
		})
	}
	if !a.allowTailscale {
		ui.Text(c, a.tr("在设置中开启 Tailscale 连接后，可添加或使用此地址。")).FontSize(12).TextColor(c.Theme().TextMuted)
	}
}

func (a *app) connectTailscaleAddress(id string) {
	if !a.isPaired(id) {
		a.reportStatusError("设备已解除配对，请重新添加设备")
		return
	}
	addr, err := normalizePeer(a.editTailscaleAddr)
	if err != nil {
		a.reportStatusError(err.Error())
		return
	}
	a.startConnection(lan.Endpoint{Addr: addr, Route: lan.RouteTailscale}, id, true)
}

func (a *app) removeTailscaleAddress(id string) {
	if err := a.engine.RemoveTailscaleAddress(id); err != nil {
		a.reportStatusError("删除 Tailscale 地址失败，请检查数据目录权限后重试")
		return
	}
	a.devices = a.engine.Peers.List()
	if a.targetPeerID == id {
		if link, online := a.engine.Link(); online && link.Route != lan.RouteTailscale {
			a.dialProposed = lan.Endpoint{}
			a.refreshConnectionCandidates()
		} else {
			a.stopDial()
			if p, ok := a.engine.Peers.Get(id); ok && p.LANAddress() != "" {
				a.startConnection(lan.Endpoint{Addr: p.LANAddress(), Route: lan.RouteLAN}, id, false)
			} else {
				a.peer = ""
			}
		}
	}
	a.editTailscaleID, a.editTailscaleAddr = "", ""
	a.status = "已删除 Tailscale 地址，设备配对仍保留"
}

func (a *app) cancelProbe() {
	a.probeSeq++
	if a.probeCancel != nil {
		a.probeCancel()
		a.probeCancel = nil
	}
}

func (a *app) probe() {
	addr, err := normalizePeer(a.peer)
	if err != nil || addr == "" {
		a.reportStatusError("先填写对端地址")
		return
	}
	ep := lan.Endpoint{Addr: addr, Route: lan.RouteLAN}
	if a.addressRoute == 1 {
		ep.Route = lan.RouteTailscale
	}
	if err := ep.Validate(); err != nil {
		a.reportStatusError(err.Error())
		return
	}
	a.cancelProbe()
	parent := a.root
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	a.probeCancel = cancel
	seq := a.probeSeq
	enabled := a.allowTailscale
	a.status = "正在测试" + ep.Route.Label() + "地址 " + addr
	go func() {
		defer cancel()
		conn, bind, err := lan.DialContext(ctx, ep, enabled, 3*time.Second)
		_, port, _ := net.SplitHostPort(addr)
		msg := "TCP 端口已连通，尚未验证设备身份；同步前仍需完成双方配对确认"
		if bind != nil {
			msg = "已通过" + ep.Route.Label() + "地址 " + bind.String() + " 拨出。" + msg
		}
		if err != nil {
			msg = fmt.Sprintf("%s连接测试失败：%s。请确认对端运行，并检查到 TCP %s 的访问权限", ep.Route.Label(), err, port)
		} else {
			_ = conn.Close()
		}
		a.update(func() {
			if a.probeSeq == seq {
				a.probeCancel = nil
				a.status = msg
				if err != nil {
					a.reportStatusError(msg)
				} else {
					a.toast(msg)
				}
			}
		})
	}()
}
