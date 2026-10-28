package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/lan"
	clipsync "github.com/2fa0/snapover/internal/sync"
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

func (a *app) connectionCandidates() []lan.Endpoint {
	p, paired := a.engine.Peers.Get(a.targetPeerID)
	if !paired {
		if a.dialProposed.Addr == "" || (a.dialProposed.Route == lan.RouteTailscale && !a.allowTailscale) {
			return nil
		}
		return []lan.Endpoint{a.dialProposed}
	}
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
	var out []lan.Endpoint
	for _, found := range a.discovered {
		if found.ID == a.targetPeerID {
			out = append(out, lan.Endpoint{Addr: found.Addr, Route: lan.RouteLAN})
			break
		}
	}
	return append(out, p.Endpoints(a.allowTailscale)...)
}

func (a *app) refreshConnectionCandidates() {
	if a.dialTarget != nil {
		a.dialTarget.Update(a.connectionCandidates())
	}
}

func (a *app) connect() {
	addr, err := normalizePeer(a.peer)
	if err != nil || addr == "" {
		a.status = "请填写对端地址，端口可省略"
		return
	}
	explicit := a.startupPeerExplicit
	a.startupPeerExplicit = false
	a.startConnection(a.endpointForAddress(addr), a.targetPeerID, explicit)
}

func (a *app) connectAddress() {
	addr, err := normalizePeer(a.peer)
	if err != nil || addr == "" {
		a.status = "请填写对端地址，端口可省略"
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
		a.status = err.Error()
		return
	}
	if ep.Route == lan.RouteTailscale {
		if !a.allowTailscale {
			a.status = lan.ErrTailscaleDisabled.Error()
			return
		}
		if err := lan.CheckTailscaleListener(a.listen); err != nil {
			a.status = err.Error()
			return
		}
	}
	if peerID == "" {
		peerID = a.peerIDForAddress(ep.Addr)
	}
	a.cancelProbe()
	a.stopDial()
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
	target := clipsync.NewDialTarget(peerID, a.dialSeq, a.connectionCandidates())
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
	if settingSwitch(c, &a.allowTailscale, "允许通过 Tailscale 连接", "两台电脑需自行安装并连接 Tailscale，并在此开启。") {
		a.saveTailscaleSetting()
	}
	if a.tailscaleError != "" {
		ui.Text(c, a.tailscaleError).FontSize(12).TextColor(c.Theme().Danger)
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
		kv(c, "局域网地址", addr)
	}
	if addr := dev.Peer.TailscaleAddr; addr != "" {
		kv(c, "Tailscale 地址", addr)
	}
	if link, up := a.engine.Link(); up && link.ID == dev.ID {
		kv(c, "当前连接", link.Route.Label())
	}
	if a.editTailscaleID == dev.ID {
		ui.Text(c, "填写该设备的 Tailscale IPv4，验证设备身份后保存。").FontSize(12).TextColor(c.Theme().TextMuted)
		submit := ui.TextInput(c.Key("tailscale-address"), &a.editTailscaleAddr).Label("Tailscale IPv4 地址").Placeholder("100.x.y.z，端口可省略").Submitted()
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "验证并连接").Disabled(waiting || !a.allowTailscale).Clicked() || (submit && !waiting && a.allowTailscale) {
				a.connectTailscaleAddress(dev.ID)
			}
			if ui.Button(c, "取消编辑").Clicked() {
				a.editTailscaleID, a.editTailscaleAddr = "", ""
			}
		})
	} else {
		ui.Row(c).Gap(8).Children(func() {
			label := "添加 Tailscale 地址"
			if dev.Peer.TailscaleAddr != "" {
				label = "修改 Tailscale 地址"
			}
			if ui.Button(c, label).Disabled(waiting || !a.allowTailscale).Clicked() {
				a.editTailscaleID, a.editTailscaleAddr = dev.ID, dev.Peer.TailscaleAddr
			}
			if dev.Peer.TailscaleAddr != "" && ui.Button(c, "删除 Tailscale 地址").Disabled(waiting).Clicked() {
				a.removeTailscaleAddress(dev.ID)
			}
		})
	}
	if !a.allowTailscale {
		ui.Text(c, "在设置中开启 Tailscale 连接后，可添加或使用此地址。").FontSize(12).TextColor(c.Theme().TextMuted)
	}
}

func (a *app) connectTailscaleAddress(id string) {
	if !a.isPaired(id) {
		a.status = "设备已解除配对，请重新添加设备"
		return
	}
	addr, err := normalizePeer(a.editTailscaleAddr)
	if err != nil {
		a.status = err.Error()
		return
	}
	a.startConnection(lan.Endpoint{Addr: addr, Route: lan.RouteTailscale}, id, true)
}

func (a *app) removeTailscaleAddress(id string) {
	if err := a.engine.RemoveTailscaleAddress(id); err != nil {
		a.status = "删除 Tailscale 地址失败，请检查数据目录权限后重试"
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
		a.status = "先填写对端地址"
		return
	}
	ep := lan.Endpoint{Addr: addr, Route: lan.RouteLAN}
	if a.addressRoute == 1 {
		ep.Route = lan.RouteTailscale
	}
	if err := ep.Validate(); err != nil {
		a.status = err.Error()
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
			}
		})
	}()
}
