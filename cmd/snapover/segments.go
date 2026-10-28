package main

import (
	"sort"
	"strings"

	"github.com/2fa0/snapover/internal/discovery"
	"github.com/2fa0/snapover/internal/lan"
)

type segmentRow struct {
	key     string
	label   string
	virtual bool
	checked bool
}

func liveSegmentRows() []segmentRow {
	return segmentRows(lan.IPv4())
}

func segmentRows(addrs []lan.Addr) []segmentRow {
	type group struct {
		addr lan.Addr
		ips  []string
	}
	order := []string{}
	groups := map[string]*group{}
	var virtual []segmentRow
	for _, addr := range addrs {
		if addr.Virtual {
			virtual = append(virtual, segmentRow{key: lan.SegmentKey(addr), label: segmentLabel(addr, []string{addr.IP.String()}), virtual: true})
			continue
		}
		key := lan.SegmentKey(addr)
		g, ok := groups[key]
		if !ok {
			groups[key] = &group{addr: addr, ips: []string{addr.IP.String()}}
			order = append(order, key)
			continue
		}
		g.ips = append(g.ips, addr.IP.String())
	}
	sort.Strings(order)
	rows := make([]segmentRow, 0, len(order)+len(virtual))
	for _, key := range order {
		g := groups[key]
		rows = append(rows, segmentRow{key: key, label: segmentLabel(g.addr, g.ips)})
	}
	sort.Slice(virtual, func(i, j int) bool { return virtual[i].label < virtual[j].label })
	return append(rows, virtual...)
}

func segmentLabel(addr lan.Addr, ips []string) string {
	cidr := ""
	if addr.IP != nil {
		cidr = addr.IP.String()
	}
	if addr.Net != nil {
		cidr = addr.Net.String()
	}
	shown := make([]string, 0, len(ips))
	seen := map[string]bool{}
	for _, ip := range ips {
		if ip == "" || ip == "<nil>" || seen[ip] {
			continue
		}
		seen[ip] = true
		shown = append(shown, ip)
	}
	label := cidr + " · " + addr.Name
	if len(shown) > 0 {
		label += " · 本机 " + strings.Join(shown, "、")
	}
	if addr.Virtual {
		label += " · " + virtualConnectionHint(addr)
	}
	return label
}

func virtualConnectionHint(addr lan.Addr) string {
	if lan.TailscaleInterface(addr) {
		return "仅用于 Tailscale 地址连接（需开启），不参与发现"
	}
	return "不用于连接"
}

func (a *app) selection() discovery.Selection {
	if a.cfg == nil {
		return discovery.SelectAll()
	}
	return discovery.Remembered(a.cfg.Values.DiscoveryMode, a.cfg.Values.DiscoveryNets, a.cfg.Values.DiscoverySeen)
}

func (a *app) currentDiscoveryScope() discovery.Scope {
	loaded := a.discoveryScope.Load()
	if loaded == nil {
		return discovery.Scope{All: true}
	}
	return loaded.(discovery.Scope)
}

func (a *app) setDiscovery(sel discovery.Selection) {
	if a.cfg == nil {
		return
	}
	previous := a.cfg.Values
	a.cfg.Values.DiscoveryMode = sel.Mode
	a.cfg.Values.DiscoveryNets = sel.Nets
	a.cfg.Values.DiscoverySeen = sel.Seen
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values = previous
		a.status = "保存设置失败"
		return
	}
	a.discoveryScope.Store(sel.Scope())
	if a.discoveryWake != nil {
		select {
		case a.discoveryWake <- struct{}{}:
		default:
		}
	}
	switch {
	case sel.All():
		a.status = "已在全部物理网段广播"
	case sel.Stopped():
		a.status = "已停止广播和搜索，仍可手填地址"
	default:
		a.status = "已更新广播网段"
	}
	if a.engine != nil && a.engine.Log != nil {
		a.engine.Log.Info("更新广播网段", "all", sel.All(), "nets", sel.Nets)
	}
}

func (a *app) syncSegmentChecks(rows []segmentRow, sel discovery.Selection) {
	same := len(a.segments) == len(rows)
	if same {
		for i := range rows {
			if a.segments[i].key != rows[i].key {
				same = false
				break
			}
		}
	}
	if same {
		for i := range rows {
			a.segments[i].label = rows[i].label
			a.segments[i].checked = sel.Scope().Allows(rows[i].key)
		}
		return
	}
	next := make([]segmentRow, len(rows))
	copy(next, rows)
	for i := range next {
		next[i].checked = sel.Scope().Allows(next[i].key)
	}
	a.segments = next
}
