package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/2fa0/clipbridge/internal/clip"
	"github.com/2fa0/clipbridge/internal/history"
	"github.com/2fa0/clipbridge/internal/identity"
	clipsync "github.com/2fa0/clipbridge/internal/sync"
)

const defaultPort = "47821"

func main() {
	listen := flag.String("listen", ":"+defaultPort, "监听地址")
	peer := flag.String("peer", "", "对端地址，例如 192.168.1.20:47821。只写 IP 时端口用 47821")
	data := flag.String("data", "", "数据目录。默认 macOS 用 ~/Library/Application Support/ClipBridge，Windows 用 %AppData%\\ClipBridge")
	reset := flag.Bool("reset-peers", false, "清除已信任的对端，下次连接重新信任")
	check := flag.Bool("check", false, "打印当前剪贴板后退出，不建立连接")
	debug := flag.Bool("debug", false, "输出调试日志")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "ClipBridge 第一批：局域网同步文字和图片。\n\n")
		fmt.Fprintf(os.Stderr, "两边都运行，并且至少一边带 -peer。建议两边都带，断线后才能互相重连。\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		log.Error("这一批只支持 Windows 10/11 和 macOS", "os", runtime.GOOS)
		os.Exit(2)
	}

	dataDir := *data
	if dataDir == "" {
		var err error
		dataDir, err = defaultDataDir()
		if err != nil {
			log.Error("无法确定数据目录", "err", err)
			os.Exit(1)
		}
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Error("创建数据目录失败", "dir", dataDir, "err", err)
		os.Exit(1)
	}

	id, err := identity.LoadOrCreate(dataDir)
	if err != nil {
		log.Error("加载本机身份失败", "err", err)
		os.Exit(1)
	}
	peers, err := clipsync.OpenStore(dataDir)
	if err != nil {
		log.Error("加载信任列表失败", "err", err)
		os.Exit(1)
	}
	if *reset {
		if err := peers.Reset(); err != nil {
			log.Error("清除信任列表失败", "err", err)
			os.Exit(1)
		}
		log.Info("已清除信任列表")
	}

	peerAddr, err := normalizePeer(*peer)
	if err != nil {
		log.Error("对端地址无效", "err", err)
		os.Exit(2)
	}

	log.Info("ClipBridge",
		"device", id.DeviceID,
		"fingerprint", identity.FormatFingerprint(id.Fingerprint),
		"data", dataDir,
		"platform", platformName(),
	)
	for _, addr := range lanIPv4() {
		log.Info("本机地址", "iface", addr.name, "suggest", net.JoinHostPort(addr.ip, portOf(*listen)))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mygo.App.SetName("ClipBridge")
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { cancel() })

	hist, err := history.Open(filepath.Join(dataDir, "history"))
	if err != nil {
		log.Error("加载复制历史失败", "err", err)
		os.Exit(1)
	}
	engine := &clipsync.Engine{
		LocalID:     id.DeviceID,
		Name:        hostname(),
		Platform:    platformName(),
		Fingerprint: id.Fingerprint,
		TLS:         identity.TLSConfig(id),
		Peers:       peers,
		Board:       clip.MyGoBoard{},
		Guard:       clip.NewEchoGuard(5 * time.Second),
		Log:         log,
	}
	uiApp := newApp(engine, hist, id, *listen, lanAddrs(*listen), peerAddr)
	engine.OnNotice = uiApp.onNotice

	mygo.App.WhenReady(func() {
		if *check {
			runCheck(log, engine.Board)
			mygo.App.Quit()
			return
		}
		uiApp.open(ctx, peerAddr)
		go func() {
			if err := clipsync.Listen(ctx, *listen, engine); err != nil && ctx.Err() == nil {
				log.Error("监听失败", "addr", *listen, "err", err)
				uiApp.onNotice(clipsync.Notice{Kind: clipsync.NoticeError, Message: "监听失败：" + err.Error()})
			}
		}()
		go clip.Watch(ctx, engine.Board, 300*time.Millisecond, engine.HandleLocal)
	})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sig:
			cancel()
			mygo.App.Quit()
		case <-ctx.Done():
		}
	}()

	if err := mygo.App.Run(); err != nil {
		log.Error("退出", "err", err)
		os.Exit(1)
	}
}

func runCheck(log *slog.Logger, board clip.Board) {
	seq, err := board.Sequence()
	if err != nil {
		log.Error("读剪贴板序号失败", "err", err)
		return
	}
	item, err := board.Snapshot()
	if err != nil {
		log.Error("读剪贴板失败", "err", err)
		return
	}
	switch {
	case item.Sensitive:
		log.Info("当前剪贴板是敏感内容，不会同步", "mark", item.Skip, "seq", seq)
	case item.Skip != "":
		log.Info("当前剪贴板无法同步", "reason", item.Skip, "seq", seq)
	case item.Kind == clip.KindText:
		preview := item.Text
		if len(preview) > 80 {
			preview = preview[:80] + "…"
		}
		log.Info("当前是文本", "bytes", item.Size, "preview", preview, "seq", seq)
	case item.Kind == clip.KindImage:
		log.Info("当前是图片", "bytes", item.Size, "width", item.Width, "height", item.Height, "seq", seq)
	default:
		log.Info("剪贴板没有可同步的文字或图片", "seq", seq)
	}
}

func defaultDataDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "ClipBridge"), nil
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			return "", fmt.Errorf("APPDATA 未设置")
		}
		return filepath.Join(base, "ClipBridge"), nil
	default:
		return "", fmt.Errorf("不支持 %s", runtime.GOOS)
	}
}

func normalizePeer(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s, nil
	}
	if strings.Contains(s, ":") {
		return "", fmt.Errorf("-peer 需要 host:port，例如 192.168.1.20:47821")
	}
	return net.JoinHostPort(s, defaultPort), nil
}

func portOf(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return defaultPort
	}
	return port
}

type ifaceIP struct {
	name string
	ip   string
}

func lanIPv4() []ifaceIP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []ifaceIP
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
			out = append(out, ifaceIP{name: iface.Name, ip: ipnet.IP.String()})
		}
	}
	return out
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "clipbridge"
	}
	return name
}

func platformName() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return runtime.GOOS
}
