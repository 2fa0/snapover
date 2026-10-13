// Package lan classifies local IPv4 interfaces so discovery and connections
// stay on the physical network. Diagnostics can still list virtual adapters.
package lan

import (
	"net"
	"strings"
	"time"
)

// Addr is one up, non-loopback IPv4 address.
type Addr struct {
	Name    string
	IP      net.IP
	Net     *net.IPNet
	Virtual bool
}

// SegmentKey identifies an IPv4 subnet on an interface. The host address is
// omitted so a DHCP renewal does not drop a saved discovery selection.
func SegmentKey(addr Addr) string {
	if addr.Net == nil {
		return addr.Name + "/"
	}
	return addr.Name + "/" + addr.Net.String()
}

// Virtual reports whether name belongs to a tunnel, VPN, or other adapter
// that should not be used for discovery or connections.
func Virtual(name string) bool {
	n := strings.ToLower(name)
	for _, part := range virtualParts {
		if strings.Contains(n, part) {
			return true
		}
	}
	return false
}

// virtualParts matches the adapters the settings page already labels.
// Company VPN clients are included so they are excluded the same way.
var virtualParts = []string{
	"utun", "tun", "tap", "wg", "tailscale", "docker", "vmnet", "vethernet",
	"bridge", "clash", "awdl", "llw", "ppp", "ipsec", "gpd", "anyconnect",
	"cisco", "pulse", "forti", "wintun", "zerotier", "nordlynx", "wireguard",
	"vpn", "hamachi", "singbox", "sing-box", "warp",
}

// IPv4 lists up, non-loopback IPv4 addresses. Virtual adapters are included
// and marked so the diagnostics page can show them.
func IPv4() []Addr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Addr
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil || ipnet.IP.IsLoopback() {
				continue
			}
			ip := ipnet.IP.To4()
			mask := ipnet.Mask
			if len(mask) == 16 {
				mask = mask[12:]
			}
			network := &net.IPNet{IP: ip.Mask(mask), Mask: mask}
			out = append(out, Addr{
				Name:    iface.Name,
				IP:      ip,
				Net:     network,
				Virtual: Virtual(iface.Name),
			})
		}
	}
	return out
}

// AllowLocal reports whether a connection accepted on local should be kept.
// Loopback and unclassified addresses are allowed. An address that belongs
// to a virtual adapter is not.
func AllowLocal(local net.IP) bool {
	return allowLocal(local, IPv4())
}

func allowLocal(local net.IP, ifaces []Addr) bool {
	if local == nil || local.IsLoopback() {
		return true
	}
	local = local.To4()
	if local == nil {
		return true
	}
	for _, iface := range ifaces {
		if iface.IP.Equal(local) {
			return !iface.Virtual
		}
	}
	return true
}

// BindIP is the physical address an outgoing connection to host should use.
// Loopback is left unbound. When host is on a physical subnet, that subnet's
// address is returned. Otherwise the first physical address is used so the
// dial does not leave through a VPN.
func BindIP(host string) net.IP {
	return bindIP(host, IPv4())
}

func bindIP(host string, ifaces []Addr) net.IP {
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() {
		return nil
	}
	ip = ip.To4()
	if ip == nil {
		return nil
	}
	var fallback net.IP
	for _, iface := range ifaces {
		if iface.Virtual || iface.IP == nil {
			continue
		}
		if fallback == nil {
			fallback = iface.IP
		}
		if iface.Net != nil && iface.Net.Contains(ip) {
			return iface.IP
		}
	}
	return fallback
}

// DialTimeout dials addr. Non-loopback targets are bound to a physical NIC
// when one exists. The returned IP is that bind address, or nil.
func DialTimeout(addr string, timeout time.Duration) (net.Conn, net.IP, error) {
	d := net.Dialer{Timeout: timeout}
	var bind net.IP
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		bind = BindIP(host)
		if bind != nil {
			d.LocalAddr = &net.TCPAddr{IP: bind}
		}
	}
	conn, err := d.Dial("tcp", addr)
	return conn, bind, err
}

// LocalIP reads the local side of a TCP connection.
func LocalIP(conn net.Conn) net.IP {
	if conn == nil || conn.LocalAddr() == nil {
		return nil
	}
	if tcp, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		return tcp.IP
	}
	host, _, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		return net.ParseIP(conn.LocalAddr().String())
	}
	return net.ParseIP(host)
}
