// Package discovery advertises routing hints on physical LAN interfaces.
// A discovered identity is verified against the TLS certificate on connection.
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
}

// Run rebinds discovery when physical interfaces change, including Wi-Fi/IP
// changes after wake. Each browse is bounded and closes its multicast sockets.
func Run(ctx context.Context, local Device, listen string, publish func(Snapshot)) {
	port, err := strconv.Atoi(local.Addr)
	if err != nil || port < 1 || port > 65535 {
		publish(Snapshot{Message: "自动发现未启动：监听端口无效，请使用手填地址"})
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
		ifaces, addrs, ips, next := interfaces(listen)
		message := ""
		if len(ifaces) != 0 {
			if err := probeMulticast(ifaces); err != nil {
				message = "局域网广播不可达。请检查系统的本地网络权限及 VPN；也可手填地址连接"
			}
		}
		if next != signature || server == nil {
			if server != nil {
				server.Shutdown()
				server = nil
			}
			clear(seen)
			signature = next
			if len(ifaces) != 0 && message == "" {
				server, err = zeroconf.RegisterProxy(local.ID, service, "local.", port, "snapover-"+local.ID, ips,
					[]string{"id=" + local.ID, "name=" + shortName(local.Name), "platform=" + local.Platform}, ifaces)
				if err != nil {
					message = "本机设备广播未启动，请检查网络权限；仍可手填地址连接"
				}
			}
		}
		if len(ifaces) == 0 {
			message = "没有可用的局域网网卡；连接 Wi-Fi 或网线后会自动搜索"
		} else if message == "" {
			resolver, err := zeroconf.NewResolver(zeroconf.SelectIfaces(ifaces), zeroconf.SelectIPTraffic(zeroconf.IPv4))
			if err != nil {
				message = "局域网搜索未启动，请检查网络权限；仍可手填地址连接"
			} else {
				browseCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
				entries := make(chan *zeroconf.ServiceEntry, 32)
				err = resolver.Browse(browseCtx, service, "local.", entries)
				if err != nil {
					message = "局域网搜索失败，请检查网络权限；仍可手填地址连接"
				}
				for entry := range entries {
					dev, ok := parseEntry(entry, local.ID, addrs)
					if !ok {
						continue
					}
					if _, exists := seen[dev.ID]; !exists && len(seen) >= maxDevices {
						continue
					}
					seen[dev.ID] = seenDevice{Device: dev, at: time.Now()}
					publish(snapshot(seen, message, time.Now()))
				}
				cancel()
			}
		}
		publish(snapshot(seen, message, time.Now()))
		timer := time.NewTimer(6 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
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
			lastErr = err
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

func interfaces(listen string) ([]net.Interface, []lan.Addr, []string, string) {
	host, _, _ := net.SplitHostPort(listen)
	bound := net.ParseIP(host)
	var ifaces []net.Interface
	var addrs []lan.Addr
	var ips, keys []string
	used := make(map[string]bool)
	for _, addr := range lan.IPv4() {
		if addr.Virtual || (bound != nil && !bound.IsUnspecified() && !bound.Equal(addr.IP)) {
			continue
		}
		iface, err := net.InterfaceByName(addr.Name)
		if err != nil || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		if !used[addr.Name] {
			ifaces = append(ifaces, *iface)
			used[addr.Name] = true
		}
		addrs = append(addrs, addr)
		ips = append(ips, addr.IP.String())
		keys = append(keys, fmt.Sprintf("%d/%s/%s", iface.Index, addr.IP, addr.Net))
	}
	sort.Strings(keys)
	return ifaces, addrs, ips, strings.Join(keys, ",")
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
