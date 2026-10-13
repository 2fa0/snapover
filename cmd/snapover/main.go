package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/crashlog"
	"github.com/2fa0/snapover/internal/front"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/lan"
	"github.com/2fa0/snapover/internal/settings"
	clipsync "github.com/2fa0/snapover/internal/sync"
)

const defaultPort = "47821"

func main() {
	listen := flag.String("listen", ":"+defaultPort, "监听地址")
	peer := flag.String("peer", "", "对端地址，例如 192.168.1.20:47821。只写 IP 时端口用 47821")
	data := flag.String("data", "", "数据目录。默认 macOS 用 ~/Library/Application Support/Snapover，Windows 用 %AppData%\\Snapover")
	reset := flag.Bool("reset-peers", false, "清除已信任的对端，下次连接重新信任")
	check := flag.Bool("check", false, "打印当前剪贴板后退出，不建立连接")
	debug := flag.Bool("debug", false, "输出调试日志")
	sendFile := flag.String("send-file", "", "Windows：直接发送一个文件到已连接的设备")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Snapover 第一批：局域网同步文字和图片。\n\n")
		fmt.Fprintf(os.Stderr, "两边都运行，并且至少一边带 -peer。建议两边都带，断线后才能互相重连。\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	mygo.App.SetName("Snapover")
	if *sendFile != "" && runtime.GOOS != "windows" {
		log.Error("--send-file 是 Windows 专属入口，请从「发送文件」窗口选择文件")
		die(2)
	}
	// Forward shell requests before touching the running instance's data or logs.
	if runtime.GOOS == "windows" && !*check && !mygo.App.RequestSingleInstanceLock() {
		return
	}

	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		log.Error("这一批只支持 Windows 10/11 和 macOS", "os", runtime.GOOS)
		die(2)
	}

	dataDir := *data
	if dataDir == "" {
		var err error
		dataDir, err = defaultDataDir()
		if err != nil {
			log.Error("无法确定数据目录", "err", err)
			die(1)
		}
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Error("创建数据目录失败", "dir", dataDir, "err", err)
		die(1)
	}
	crashPath, crashErr := crashlog.Install(dataDir)
	log = newLogger(dataDir, level)
	if crashErr != nil {
		log.Error("崩溃日志不可用", "err", crashErr)
	} else {
		log.Info("崩溃日志", "path", crashPath)
	}

	id, err := identity.LoadOrCreate(dataDir)
	if err != nil {
		log.Error("加载本机身份失败", "err", err)
		die(1)
	}
	peers, err := clipsync.OpenStore(dataDir)
	if err != nil {
		log.Error("加载信任列表失败", "err", err)
		die(1)
	}
	if *reset {
		if err := peers.Reset(); err != nil {
			log.Error("清除信任列表失败", "err", err)
			die(1)
		}
		log.Info("已清除信任列表")
	}

	peerAddr, err := normalizePeer(*peer)
	if err != nil {
		log.Error("对端地址无效", "err", err)
		die(2)
	}

	log.Info("Snapover",
		"device", id.DeviceID,
		"fingerprint", identity.FormatFingerprint(id.Fingerprint),
		"data", dataDir,
		"platform", platformName(),
	)
	for _, addr := range lan.IPv4() {
		if addr.Virtual {
			log.Info("跳过虚拟网卡", "iface", addr.Name, "ip", addr.IP)
			continue
		}
		log.Info("本机地址", "iface", addr.Name, "suggest", net.JoinHostPort(addr.IP.String(), portOf(*listen)))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {})

	hist, err := history.Open(filepath.Join(dataDir, "history"))
	if err != nil {
		log.Error("加载复制历史失败", "err", err)
		die(1)
	}
	cfg, err := settings.Load(dataDir)
	if err != nil {
		log.Error("加载设置失败", "err", err)
		die(1)
	}
	engine := &clipsync.Engine{
		LocalID:     id.DeviceID,
		Name:        hostname(),
		Platform:    platformName(),
		Fingerprint: id.Fingerprint,
		ListenPort:  portOf(*listen),
		TLS:         identity.TLSConfig(id),
		Peers:       peers,
		Board:       clip.MyGoBoard{},
		RunOnBoard:  mygo.RunOnMain,
		Guard:       clip.NewEchoGuard(5 * time.Second),
		Log:         log,
	}
	engine.SetPolicy(cfg.Values.Paused, cfg.Values.TextOnly, cfg.Values.MaxImageMB<<20)
	receiveDir := cfg.Values.ReceiveDir
	if receiveDir == "" {
		receiveDir = defaultReceiveDir()
	}
	engine.SetReceiveDir(receiveDir)
	uiApp := newApp(engine, hist, cfg, id, *listen, lanAddrs(*listen), peerAddr)
	if runtime.GOOS == "windows" {
		if *sendFile != "" {
			uiApp.pendingFileRequest = &fileRequest{paths: append([]string{*sendFile}, flag.Args()...), cold: true}
		}
		mygo.App.OnSecondInstance(func(args []string, workingDir string) {
			if paths, requested := sendToPaths(args, workingDir); requested {
				uiApp.handleFileRequest(paths, false)
			} else if uiApp.win != nil {
				uiApp.reveal(uiApp.page)
			}
		})
	}
	engine.OnNotice = uiApp.onNotice
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		uiApp.quitting = true
		cancel()
	})

	mygo.App.WhenReady(func() {
		if *check {
			runCheck(log, engine.Board)
			mygo.App.Quit()
			return
		}
		uiApp.open(ctx)
		go func() {
			if err := clipsync.Listen(ctx, *listen, engine); err != nil && ctx.Err() == nil {
				log.Error("监听失败", "addr", *listen, "err", err)
				uiApp.onNotice(clipsync.Notice{Kind: clipsync.NoticeError, Message: "监听失败：" + err.Error()})
			}
		}()
		go clip.Watch(ctx, engine.Board, clip.PollEvery, func(seq uint64, item clip.Item) {
			if item.Kind != "" && !item.Sensitive && item.Skip == "" && item.SourceApp == "" {
				item.SourceApp = clip.CleanSource(front.Current())
			}
			engine.HandleLocal(seq, item)
		})
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
		die(1)
	}
	crashlog.MarkClean()
}

func die(code int) {
	crashlog.MarkClean()
	os.Exit(code)
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
	if dir, ok := portableDataDir(); ok {
		return dir, nil
	}
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "Snapover"), nil
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			return "", fmt.Errorf("APPDATA 未设置")
		}
		return filepath.Join(base, "Snapover"), nil
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

func portableDataDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	if _, err := os.Stat(filepath.Join(dir, "portable.flag")); err != nil {
		return "", false
	}
	return filepath.Join(dir, "data"), true
}

func newLogger(dataDir string, level slog.Level) *slog.Logger {
	w := io.Writer(os.Stdout)
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err == nil {
		path := filepath.Join(dir, "snapover.log")
		rotateLog(path)
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			w = io.MultiWriter(os.Stdout, f)
		}
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

func rotateLog(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < 1<<20 {
		return
	}
	_ = os.Rename(path, path+".1")
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "snapover"
	}
	return name
}

func platformName() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return runtime.GOOS
}
