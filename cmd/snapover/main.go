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

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/control"
	"github.com/2fa0/snapover/internal/crashlog"
	"github.com/2fa0/snapover/internal/filetransfer"
	"github.com/2fa0/snapover/internal/front"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/lan"
	"github.com/2fa0/snapover/internal/settings"
	clipsync "github.com/2fa0/snapover/internal/sync"
	"github.com/egoist/mygo"
)

const defaultPort = "47821"

func main() {
	listen := flag.String("listen", ":"+defaultPort, "监听地址")
	peer := flag.String("peer", "", "对端地址，例如 192.168.1.20:47821。只写 IP 时端口用 47821")
	data := flag.String("data", "", "数据目录。默认 macOS 用 ~/Library/Application Support/Snapover，Windows 用 %AppData%\\Snapover")
	reset := flag.Bool("reset-peers", false, "解除全部配对，下次添加需双方确认")
	check := flag.Bool("check", false, "检查当前剪贴板类型和大小后退出，不建立连接")
	debug := flag.Bool("debug", false, "输出调试日志")
	sendFile := flag.String("send-file", "", "直接发送一个文件到已连接的设备")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Snapover：局域网同步文字、图片、文件和文件夹。\n\n")
		fmt.Fprintf(os.Stderr, "两边都运行，在「连接设备」中点击「添加设备」，核对校验码并确认。也可用 -peer 填写对端地址。\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	mygo.App.SetName("Snapover")
	// Diagnostics must not open or maintain the running app's data directory.
	if *check {
		if err := runCheckMode(log, clip.MyGoBoard{}); err != nil {
			log.Error("检查剪贴板失败", "err", err)
		}
		return
	}
	// The update plugin must be registered before App.Run, and its window is
	// the only place a new version is offered.
	useUpdater()
	// Forward shell requests before touching the running instance's data or logs.
	if (runtime.GOOS == "windows" || runtime.GOOS == "darwin") && !mygo.App.RequestSingleInstanceLock() {
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
	mygo.App.SetPath(mygo.PathUserData, dataDir)
	removedLogs := crashlog.Maintain(dataDir)
	crashPath, crashErr := crashlog.Install(dataDir)
	log = newLogger(dataDir, level)
	if removedLogs > 0 {
		log.Info("已清理过期日志", "removed", removedLogs)
	}
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
		if lan.TailscaleInterface(addr) {
			log.Info("本机 Tailscale 地址（需在设置中开启连接）", "iface", addr.Name, "suggest", net.JoinHostPort(addr.IP.String(), portOf(*listen)))
			continue
		}
		if addr.Virtual {
			log.Info("跳过虚拟网卡", "iface", addr.Name, "ip", addr.IP)
			continue
		}
		log.Info("本机地址", "iface", addr.Name, "suggest", net.JoinHostPort(addr.IP.String(), portOf(*listen)))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n := crashlog.PruneInactive(dataDir); n > 0 {
					log.Info("已清理过期日志", "removed", n)
				}
			}
		}
	}()

	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {})

	historyDir := filepath.Join(dataDir, "history")
	hist, err := history.Open(historyDir)
	var historyOpenErr error
	if err != nil {
		log.Error("加载复制历史失败", "err", err)
		historyOpenErr = err
		hist = history.Unavailable(historyDir, err)
	}
	cfg, err := settings.Load(dataDir)
	if err != nil {
		log.Error("加载设置失败", "err", err)
		die(1)
	}
	configureLanguage(cfg.Values.Language)
	configureTheme(cfg.Values.Theme)
	historyCapacityErr := hist.SetCapacity(int64(cfg.Values.HistoryMaxMB) << 20)
	if historyCapacityErr != nil {
		log.Warn("应用历史容量上限失败", "err", historyCapacityErr)
	}
	engine := &clipsync.Engine{
		LocalID:     id.DeviceID,
		Name:        hostname(),
		Platform:    platformName(),
		Fingerprint: id.Fingerprint,
		ListenPort:  portOf(*listen),
		TLS:         identity.TLSConfig(id),
		Peers:       peers,
		RunOnBoard:  mygo.RunOnMain,
		Guard:       clip.NewEchoGuard(5 * time.Second),
		Log:         log,
	}
	engine.FileCache = filetransfer.NewClipboardCache(filepath.Join(dataDir, "clipboard-files"))
	engine.Board = clip.MyGoBoard{TextLimit: engine.TextLimit}
	engine.SetPolicy(cfg.Values.Paused, cfg.Values.TextOnly, cfg.Values.MaxImageMB<<20)
	engine.SetManualSync(cfg.Values.ManualSync)
	engine.SetPrivacyPolicy(cfg.Values.PrivacyMode, cfg.Values.ExcludedApps)
	receiveDir := cfg.Values.ReceiveDir
	if receiveDir == "" {
		receiveDir = defaultReceiveDir()
	}
	engine.SetReceiveDir(receiveDir)
	go func() {
		if err := filetransfer.CleanStaleTransfers(receiveDir); err != nil {
			log.Warn("清理过期传输临时文件失败", "err", err)
		}
	}()
	uiApp := newApp(engine, hist, cfg, id, *listen, lanAddrs(*listen), peerAddr)
	uiApp.startUpdates()
	if historyOpenErr != nil {
		uiApp.reportStatusError("历史不可用，其他功能仍可使用")
	}
	if historyCapacityErr != nil {
		uiApp.historyCapacityError = "历史维护失败，请在设置中重新应用上限：" + historyCapacityErr.Error()
	}
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows && uiApp.root != nil {
			uiApp.reveal(uiApp.page)
		}
	})
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		if *sendFile != "" {
			uiApp.pendingFileRequest = &fileRequest{paths: append([]string{*sendFile}, flag.Args()...), cold: true}
		}
		mygo.App.OnSecondInstance(uiApp.handleSecondInstance)
	}
	engine.OnNotice = uiApp.onNotice
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		uiApp.quitting = true
		cancel()
		uiApp.stopKeepAwake()
		uiApp.hist.CleanOpened()
	})

	mygo.App.WhenReady(func() {
		go engine.MaintainClipboardCache(ctx)
		// Listen before the window so Windows can show the firewall prompt while
		// the unpaired devices page is explaining that prompt.
		go func() {
			if err := clipsync.Listen(ctx, *listen, engine); err != nil && ctx.Err() == nil {
				log.Error("监听失败", "addr", *listen, "err", err)
				uiApp.onNotice(clipsync.Notice{Kind: clipsync.NoticeError, Message: "监听失败：" + err.Error()})
			}
		}()
		uiApp.open(ctx)
		if _, err := control.Start(ctx, dataDir, uiApp.handleCLI); err != nil {
			log.Error("CLI 服务启动失败", "err", err)
			uiApp.onNotice(clipsync.Notice{Kind: clipsync.NoticeError, Message: "CLI 服务启动失败：" + err.Error()})
		}
		engine.StartLocalQueue(ctx)
		go clip.Watch(ctx, engine.Board, clip.PollEvery, func(seq uint64, item clip.Item) {
			if item.Kind != "" && !item.Sensitive && item.Skip == "" && item.SourceApp == "" {
				item.SourceApp = clip.CleanSource(front.Current())
			}
			engine.ObserveLocal(seq, item)
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

func runCheckMode(log *slog.Logger, board clip.Board) error {
	// MyGo may persist its own UI preferences even without an app window.
	dir, err := os.MkdirTemp("", "snapover-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	mygo.App.SetPath(mygo.PathUserData, dir)
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.WhenReady(func() {
		runCheck(log, board)
		mygo.App.Quit()
	})
	return mygo.App.Run()
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
		log.Info("当前是文本", "bytes", item.Size, "seq", seq)
	case item.Kind == clip.KindImage:
		log.Info("当前是图片", "bytes", item.Size, "width", item.Width, "height", item.Height, "seq", seq)
	default:
		log.Info("剪贴板没有可同步的文字或图片", "seq", seq)
	}
}

func defaultDataDir() (string, error) {
	return control.DefaultDataDir()
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

func newLogger(dataDir string, level slog.Level) *slog.Logger {
	options := &slog.HandlerOptions{Level: level}
	fallback := slog.New(slog.NewTextHandler(os.Stdout, options))
	dir := filepath.Join(dataDir, "logs")
	path := filepath.Join(dir, "snapover.log")
	exe, _ := os.Executable()
	fail := func(err error) *slog.Logger {
		crashlog.Record(fmt.Sprintf("普通日志初始化失败 path=%q exe=%q pid=%d logger=file-first-v2 err=%v", path, exe, os.Getpid(), err))
		return fallback
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(err)
	}
	f, err := crashlog.OpenOrdinary(path)
	if err != nil {
		return fail(err)
	}
	crashlog.PruneInactive(dataDir)
	// Check the actual file write independently of an absent Windows GUI console.
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "普通日志已启用", 0)
	record.Add("path", path, "exe", exe, "pid", os.Getpid(), "logger", "file-first-v2")
	if err := slog.NewTextHandler(f, options).Handle(context.Background(), record); err != nil {
		_ = f.Close()
		return fail(err)
	}
	crashlog.Record(fmt.Sprintf("普通日志写入已验证 path=%q exe=%q pid=%d logger=file-first-v2", path, exe, os.Getpid()))
	// Windows GUI builds may have no stdout; write the file before it.
	return slog.New(slog.NewTextHandler(io.MultiWriter(f, os.Stdout), options))
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
