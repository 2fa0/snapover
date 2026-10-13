// Package discovery advertises routing hints on selected physical LAN
// interfaces. A discovered identity is verified against the TLS certificate
// on connection.
package discovery

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
	"github.com/miekg/dns"
	"github.com/2fa0/snapover/internal/lan"
	"golang.org/x/net/ipv4"
)

const service = "_snapover._tcp"
const staleAfter = 45 * time.Second
const maxDevices = 256

type Device struct {
	ID       string
	Name     string
	Platform string
	Addr     string
}

type seenDevice struct {
	Device
	at time.Time
}

type Snapshot struct {
	Devices []Device
	Message string
	Err     error
	// Scope identifies the selection used by this scan. A queued snapshot
	// can then be discarded if the selection changed before UI delivery.
	Scope *Scope
}

// Run rebinds discovery when the selected physical interfaces change,
// including Wi-Fi/IP changes after wake. scope is read on each pass; a nil
// scope uses every physical segment. A signal on wake rebinds without waiting
// out the browse or the idle interval. Each browse is bounded and closes its
// multicast sockets.
func Run(ctx context.Context, local Device, listen string, scope func() Scope, wake <-chan struct{}, publish func(Snapshot)) {
	port, err := strconv.Atoi(local.Addr)
	if err != nil || port < 1 || port > 65535 {
		publish(Snapshot{Message: "自动发现未启动：监听端口无效，请使用手填地址", Err: fmt.Errorf("invalid discovery port %q", local.Addr)})
		return
	}
	var server *zeroconf.Server
	defer func() {
		if server != nil {
			server.Shutdown()
		}
	}()
	var signature string
	seen := make(map[string]seenDevice)
	for ctx.Err() == nil {
		selected := readScope(scope)
		chosen := interfaces(listen, selected)
		var scanErr error
		publishSnapshot := func(message string) {
			s := snapshot(seen, message, time.Now())
			s.Scope = &selected
			s.Err = scanErr
			publish(s)
		}
		message := ""
		if len(chosen.ifaces) != 0 {
			if err := probeMulticast(chosen.ifaces); err != nil {
				scanErr = err
				message = "局域网广播不可达。请检查系统的本地网络权限及 VPN；也可手填地址连接"
			}
		}
		if chosen.signature != signature || server == nil {
			if server != nil {
				server.Shutdown()
				server = nil
			}
			clear(seen)
			signature = chosen.signature
			if len(chosen.ifaces) != 0 && message == "" {
				server, err = zeroconf.RegisterProxy(local.ID, service, "local.", port, "snapover-"+local.ID, chosen.ips,
					[]string{"id=" + local.ID, "name=" + shortName(local.Name), "platform=" + local.Platform}, chosen.ifaces)
				if err != nil {
					scanErr = fmt.Errorf("register mDNS service: %w", err)
					message = "本机设备广播未启动，请检查网络权限；仍可手填地址连接"
				}
			}
		}
		woken := false
		if len(chosen.ifaces) == 0 {
			if chosen.empty == emptyUnselected {
				message = "未选择广播网段。勾选网段后会开始搜索，也可手填地址"
			} else {
				message = "没有可用的局域网网卡；连接 Wi-Fi 或网线后会自动搜索"
			}
		} else if message == "" {
			resolver, err := zeroconf.NewResolver(zeroconf.SelectIfaces(chosen.ifaces), zeroconf.SelectIPTraffic(zeroconf.IPv4))
			if err != nil {
				scanErr = fmt.Errorf("create mDNS resolver: %w", err)
				message = "局域网搜索未启动，请检查网络权限；仍可手填地址连接"
			} else {
				browseCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
				woke := watchWake(browseCtx, cancel, wake)
				entries := make(chan *zeroconf.ServiceEntry, 32)
				err = resolver.Browse(browseCtx, service, "local.", entries)
				if err != nil && browseCtx.Err() == nil {
					scanErr = fmt.Errorf("browse mDNS service: %w", err)
					message = "局域网搜索失败，请检查网络权限；仍可手填地址连接"
				}
				for entry := range entries {
					if browseCtx.Err() != nil {
						continue
					}
					dev, ok := parseEntry(entry, local.ID, chosen.addrs)
					if !ok {
						continue
					}
					if _, exists := seen[dev.ID]; !exists && len(seen) >= maxDevices {
						continue
					}
					seen[dev.ID] = seenDevice{Device: dev, at: time.Now()}
					publishSnapshot(message)
				}
				cancel()
				woken = woke()
			}
		}
		if woken {
			continue
		}
		publishSnapshot(message)
		if !wait(ctx, 6*time.Second, wake) {
			return
		}
	}
}

func readScope(scope func() Scope) Scope {
	if scope == nil {
		return Scope{All: true}
	}
	got := scope()
	if got.All {
		return Scope{All: true}
	}
	if got.Keys == nil {
		got.Keys = map[string]bool{}
	}
	return got
}

// watchWake cancels browse when the selection changes. Call the returned
// function after cancelling browse to join the watcher before the idle wait,
// so an old watcher cannot consume the next selection change.
func watchWake(ctx context.Context, cancel context.CancelFunc, wake <-chan struct{}) func() bool {
	if wake == nil {
		return func() bool { return false }
	}
	var hit bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-wake:
			hit = true
			cancel()
		case <-ctx.Done():
		}
	}()
	return func() bool {
		<-done
		return hit
	}
}

func wait(ctx context.Context, d time.Duration, wake <-chan struct{}) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return true
	case <-timer.C:
		return true
	}
}

// The discovery library discards multicast send errors. Probe the same
// physical interfaces so denied LAN access does not look like an empty scan.
func probeMulticast(ifaces []net.Interface) error {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("224.0.0.0")})
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	query := new(dns.Msg)
	query.SetQuestion(service+".local.", dns.TypePTR)
	query.RecursionDesired = false
	packet, err := query.Pack()
	if err != nil {
		return err
	}
	pc := ipv4.NewPacketConn(conn)
	var lastErr error
	for _, iface := range ifaces {
		if _, err := pc.WriteTo(packet, &ipv4.ControlMessage{IfIndex: iface.Index}, &net.UDPAddr{IP: net.ParseIP("224.0.0.251"), Port: 5353}); err == nil {
			return nil
		} else {
			lastErr = fmt.Errorf("multicast on %s: %w", iface.Name, err)
		}
	}
	return lastErr
}

func snapshot(seen map[string]seenDevice, message string, now time.Time) Snapshot {
	s := Snapshot{Message: message}
	for id, dev := range seen {
		if now.Sub(dev.at) > staleAfter {
			delete(seen, id)
			continue
		}
		s.Devices = append(s.Devices, dev.Device)
	}
	sort.Slice(s.Devices, func(i, j int) bool { return s.Devices[i].ID < s.Devices[j].ID })
	return s
}

func parseEntry(entry *zeroconf.ServiceEntry, localID string, addrs []lan.Addr) (Device, bool) {
	if entry == nil || entry.Port < 1 || entry.Port > 65535 {
		return Device{}, false
	}
	var dev Device
	for _, txt := range entry.Text {
		key, value, ok := strings.Cut(txt, "=")
		if !ok {
			continue
		}
		switch key {
		case "id":
			dev.ID = value
		case "name":
			dev.Name = shortName(value)
		case "platform":
			dev.Platform = value
		}
	}
	if len(dev.ID) != 32 || dev.ID == localID || dev.ID != strings.ToLower(dev.ID) {
		return Device{}, false
	}
	if _, err := hex.DecodeString(dev.ID); err != nil {
		return Device{}, false
	}
	for _, ip := range entry.AddrIPv4 {
		if ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
			continue
		}
		for _, addr := range addrs {
			if !addr.Virtual && addr.Net != nil && addr.Net.Contains(ip) {
				dev.Addr = net.JoinHostPort(ip.String(), strconv.Itoa(entry.Port))
				return dev, true
			}
		}
	}
	return Device{}, false
}

const (
	emptyNone = iota + 1
	emptyUnselected
)

type chosenIfaces struct {
	ifaces    []net.Interface
	addrs     []lan.Addr
	ips       []string
	signature string
	empty     int
}

func interfaces(listen string, scope Scope) chosenIfaces {
	return chooseInterfaces(listen, scope, lan.IPv4(), net.InterfaceByName)
}

func chooseInterfaces(listen string, scope Scope, listed []lan.Addr, lookup func(string) (*net.Interface, error)) chosenIfaces {
	host, _, _ := net.SplitHostPort(listen)
	bound := net.ParseIP(host)
	var out chosenIfaces
	used := make(map[string]bool)
	var keys []string
	candidates := 0
	for _, addr := range listed {
		if addr.Virtual || (bound != nil && !bound.IsUnspecified() && !bound.Equal(addr.IP)) {
			continue
		}
		iface, err := lookup(addr.Name)
		if err != nil || iface == nil || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		candidates++
		if !scope.All && !scope.Keys[lan.SegmentKey(addr)] {
			continue
		}
		if !used[addr.Name] {
			out.ifaces = append(out.ifaces, *iface)
			used[addr.Name] = true
		}
		out.addrs = append(out.addrs, addr)
		if addr.IP != nil {
			out.ips = append(out.ips, addr.IP.String())
		}
		keys = append(keys, fmt.Sprintf("%d/%s/%s", iface.Index, addr.IP, addr.Net))
	}
	sort.Strings(keys)
	out.signature = strings.Join(keys, ",")
	if len(out.ifaces) == 0 {
		if candidates > 0 && !scope.All {
			out.empty = emptyUnselected
			out.signature = "unselected"
		} else {
			out.empty = emptyNone
		}
	}
	return out
}

func shortName(s string) string {
	runes := []rune(strings.Map(func(r rune) rune {
		if r < ' ' || r == 127 {
			return -1
		}
		return r
	}, s))
	if len(runes) > 48 {
		runes = runes[:48]
	}
	return string(runes)
}
