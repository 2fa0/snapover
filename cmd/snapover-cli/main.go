package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/2fa0/snapover/internal/control"
	"github.com/2fa0/snapover/internal/settings"
)

const usage = `用法：
  snapover-cli send file  [--timeout 60s] [--data 目录] [--] 文件路径
  snapover-cli send text  [--timeout 60s] [--data 目录] [--] "文本"
  snapover-cli send text  --stdin [--timeout 60s] [--data 目录]
  snapover-cli send image [--timeout 60s] [--data 目录] [--] 图片路径

请先启动 Snapover 并连接设备。文件保存到对端接收文件夹，文字和图片
进入对端剪贴板，不改动本机剪贴板。路径包含空格时请加引号。
文字使用 UTF-8，上限沿用 Snapover 设置（默认 1 MB，最多 20 MB）；
图片支持 PNG/JPEG/BMP/WebP，最大 20 MB；
单文件上限沿用 Snapover 设置（默认 200 MB），双方分别校验。
可设置 snapover 别名以缩短命令。
退出码：0 成功，1 发送失败，2 参数或输入无效。
`

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

type command struct {
	request control.Request
	dataDir string
	timeout time.Duration
}

func parse(ctx context.Context, args []string, stdin io.Reader) (command, error) {
	if len(args) < 2 || args[0] != "send" {
		return command{}, errors.New("请使用 send file、send text 或 send image")
	}
	kind := args[1]
	if kind != "file" && kind != "text" && kind != "image" {
		return command{}, errors.New("发送类型必须是 file、text 或 image")
	}
	flags := flag.NewFlagSet("send "+kind, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	fromStdin := flags.Bool("stdin", false, "从管道读取 UTF-8 文本")
	dataDir := flags.String("data", "", "后台 Snapover 的数据目录")
	timeout := flags.Duration("timeout", 60*time.Second, "发送超时时间")
	if err := flags.Parse(args[2:]); err != nil {
		return command{}, err
	}
	if *timeout < time.Millisecond || *timeout > control.MaxTimeout {
		return command{}, errors.New("超时时间必须在 1ms 至 10m 之间")
	}
	values := flags.Args()
	var value string
	if *fromStdin {
		if kind != "text" || len(values) != 0 {
			return command{}, errors.New("--stdin 仅用于文本，不能同时提供文本参数")
		}
		data, err := readInput(ctx, stdin)
		if err != nil {
			return command{}, fmt.Errorf("读取标准输入失败：%w", err)
		}
		if len(data) > settings.MaxTextMB<<20 {
			return command{}, fmt.Errorf("标准输入超过 %d MB", settings.MaxTextMB)
		}
		value = strings.TrimPrefix(string(data), "\ufeff")
	} else {
		if len(values) != 1 {
			return command{}, errors.New("请提供一个文本参数或一个文件路径；包含空格时请加引号")
		}
		value = values[0]
	}
	if kind == "text" {
		if value == "" || len(value) > settings.MaxTextMB<<20 || !utf8.ValidString(value) {
			return command{}, fmt.Errorf("请输入非空、%d MB 以内的 UTF-8 文本", settings.MaxTextMB)
		}
	} else {
		if value == "" {
			return command{}, errors.New("文件路径不能为空")
		}
		var err error
		value, err = filepath.Abs(value)
		if err != nil {
			return command{}, err
		}
		info, err := os.Stat(value)
		if err != nil {
			return command{}, fmt.Errorf("无法读取文件：%w", err)
		}
		if !info.Mode().IsRegular() {
			return command{}, errors.New("请选择一个普通文件，不支持文件夹")
		}
		// File size is checked against the running app's current setting.
		if kind == "image" && info.Size() > 20<<20 {
			return command{}, errors.New("请选择 20 MB 以内的普通文件")
		}
	}
	if *dataDir == "" {
		var err error
		*dataDir, err = control.DefaultDataDir()
		if err != nil {
			return command{}, err
		}
	}
	return command{request: control.Request{Kind: kind, Value: value, TimeoutMS: timeout.Milliseconds()}, dataDir: *dataDir, timeout: *timeout}, nil
}

func readInput(ctx context.Context, stdin io.Reader) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type result struct {
		data []byte
		err  error
	}
	ready := make(chan result, 1)
	// A pipe or console reader can block until EOF. Let cancellation return
	// immediately; main exits the CLI process even if that read is still blocked.
	go func() {
		data, err := io.ReadAll(io.LimitReader(stdin, (settings.MaxTextMB<<20)+1))
		ready <- result{data: data, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ready:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return r.data, r.err
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "-h") || len(args) == 2 && args[0] == "send" && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprint(stdout, usage)
		return 0
	}
	cmd, err := parse(ctx, args, stdin)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		fmt.Fprintln(stderr, "已取消发送："+err.Error())
		return 1
	}
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "参数错误："+err.Error())
		fmt.Fprint(stderr, usage)
		return 2
	}
	// Leave time for the app to report a timeout with the final delivery status.
	ctx, cancel := context.WithTimeout(ctx, cmd.timeout+3*time.Second)
	defer cancel()
	err = control.Send(ctx, cmd.dataDir, cmd.request, func(event control.Event) {
		switch event.State {
		case "started":
			fmt.Fprintln(stderr, event.Message)
		case "progress":
			if event.Total > 0 {
				fmt.Fprintf(stderr, "已传输 %.0f%%（%d / %d 字节）\n", float64(event.Done)*100/float64(event.Total), event.Done, event.Total)
			}
		case "complete":
			fmt.Fprintln(stdout, event.Message)
		}
	})
	if err != nil {
		fmt.Fprintln(stderr, "发送失败："+err.Error())
		return 1
	}
	return 0
}
