package lan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

type Route string

const (
	RouteLAN       Route = "lan"
	RouteTailscale Route = "tailscale"
)

// Endpoint carries the user's routing choice; an IP address is not an identity.
type Endpoint struct {
	Addr  string
	Route Route
}

var (
	ErrTailscaleDisabled = errors.New("尚未允许 Tailscale 连接，请在两台电脑的设置中开启")
	ErrNoTailscale       = errors.New("没有可用的 Tailscale IPv4 隧道，请先连接 Tailscale")
)

func (r Route) Label() string {
	if r == RouteTailscale {
		return "Tailscale"
	}
	return "局域网"
}

// TailscaleIP checks the shared IPv4 range, not which program owns an address.
func TailscaleIP(ip net.IP) bool {
	ip = ip.To4()
	return ip != nil && ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127
}

// TailscaleInterface requires a tunnel as well as an address in the shared range.
// macOS uses numbered utun interfaces; Windows normally calls it Tailscale.
// This remains a routing hint. TLS and explicit pairing establish trust.
func TailscaleInterface(addr Addr) bool {
	if !addr.Virtual || !TailscaleIP(addr.IP) {
		return false
	}
	n := strings.ToLower(addr.Name)
	return strings.HasPrefix(n, "tailscale") || strings.HasPrefix(n, "utun") ||
		strings.HasPrefix(n, "tun") || strings.HasPrefix(n, "wintun")
}

func (ep Endpoint) Validate() error {
	host, port, err := net.SplitHostPort(ep.Addr)
	if err != nil || host == "" {
		return errors.New("请填写有效的对端地址，端口可省略")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("端口须为 1–65535 的整数")
	}
	if ep.Route != RouteLAN && ep.Route != RouteTailscale {
		return errors.New("请选择局域网或 Tailscale 连接")
	}
	if ep.Route == RouteTailscale && !TailscaleIP(net.ParseIP(host)) {
		return errors.New("请填写 Tailscale IPv4 地址（100.64.0.0–100.127.255.255）")
	}
	return nil
}

func routeBind(ep Endpoint, enabled bool, ifaces []Addr) (net.IP, error) {
	if err := ep.Validate(); err != nil {
		return nil, err
	}
	host, _, _ := net.SplitHostPort(ep.Addr)
	if ep.Route == RouteLAN {
		return bindIP(host, ifaces), nil
	}
	if !enabled {
		return nil, ErrTailscaleDisabled
	}
	var bind net.IP
	for _, addr := range ifaces {
		if !TailscaleInterface(addr) {
			continue
		}
		if bind != nil && !bind.Equal(addr.IP) {
			return nil, errors.New("发现多个共享地址隧道，无法确定 Tailscale 路径，请检查其他 VPN")
		}
		bind = addr.IP
	}
	if bind == nil {
		return nil, ErrNoTailscale
	}
	return bind, nil
}

// DialContext uses the same routing policy for the real connection and probe.
func DialContext(ctx context.Context, ep Endpoint, enabled bool, timeout time.Duration) (net.Conn, net.IP, error) {
	bind, err := routeBind(ep, enabled, IPv4())
	if err != nil {
		return nil, nil, err
	}
	d := net.Dialer{Timeout: timeout}
	if bind != nil {
		d.LocalAddr = &net.TCPAddr{IP: bind}
	}
	conn, err := d.DialContext(ctx, "tcp", ep.Addr)
	if err == nil {
		route, routeErr := incomingRoute(LocalIP(conn), RemoteIP(conn), enabled, IPv4())
		if routeErr != nil || route != ep.Route || (ep.Route == RouteTailscale && !LocalIP(conn).Equal(bind)) {
			_ = conn.Close()
			return nil, bind, fmt.Errorf("连接没有使用预期的%s路径，请检查地址类型和 VPN 路由", ep.Route.Label())
		}
	}
	return conn, bind, err
}

func IncomingRoute(conn net.Conn, enabled bool) (Route, error) {
	return incomingRoute(LocalIP(conn), RemoteIP(conn), enabled, IPv4())
}

func incomingRoute(local, remote net.IP, enabled bool, ifaces []Addr) (Route, error) {
	for _, addr := range ifaces {
		if !addr.IP.Equal(local) || !TailscaleInterface(addr) {
			continue
		}
		if !enabled {
			return "", ErrTailscaleDisabled
		}
		if !TailscaleIP(remote) {
			return "", errors.New("Tailscale 接入的来源不是受支持的 IPv4 地址")
		}
		return RouteTailscale, nil
	}
	if !allowLocal(local, ifaces) {
		return "", errors.New("该虚拟网卡不用于连接")
	}
	return RouteLAN, nil
}

// CheckTailscaleListener respects an explicitly restricted -listen address.
func CheckTailscaleListener(listen string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("监听地址无效：%w", err)
	}
	ip := net.ParseIP(host)
	if host == "" || (ip != nil && ip.IsUnspecified()) {
		return nil
	}
	for _, addr := range IPv4() {
		if TailscaleInterface(addr) && addr.IP.Equal(ip) {
			return nil
		}
	}
	return errors.New("当前监听地址不接受 Tailscale 连接，请使用 -listen :47821 或本机 Tailscale 地址")
}

func RemoteIP(conn net.Conn) net.IP {
	if conn == nil || conn.RemoteAddr() == nil {
		return nil
	}
	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		return addr.IP
	}
	host, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	return net.ParseIP(host)
}
