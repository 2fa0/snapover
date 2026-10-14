// Package control connects the console client to the running desktop app.
package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/2fa0/snapover/internal/settings"
)

const MaxTimeout = 10 * time.Minute

// JSON escaping can expand text sixfold.
const maxRequestBytes = (settings.MaxTextMB<<20)*6 + 64<<10

type Request struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	TimeoutMS int64  `json:"timeout_ms"`
}

type Event struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Done    int64  `json:"done,omitempty"`
	Total   int64  `json:"total,omitempty"`
}

type Handler func(context.Context, Request, func(int64, int64)) (string, error)

type endpoint struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}

type Server struct {
	http *http.Server
	lock *os.File
	path string
	once sync.Once
}

func Start(ctx context.Context, dataDir string, handler Handler) (*Server, error) {
	lock, err := lockFile(filepath.Join(dataDir, "cli.lock"))
	if err != nil {
		return nil, fmt.Errorf("无法锁定 CLI 服务，可能已有 Snapover 在运行：%w", err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		lock.Close()
		return nil, err
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		ln.Close()
		lock.Close()
		return nil, err
	}
	ep := endpoint{Address: ln.Addr().String(), Token: hex.EncodeToString(token)}
	s := &Server{lock: lock, path: filepath.Join(dataDir, "cli.json")}
	busy := make(chan struct{}, 1)
	s.http = &http.Server{
		ReadTimeout: 10 * time.Second, ReadHeaderTimeout: 3 * time.Second,
		IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096,
		WriteTimeout: MaxTimeout + 15*time.Second,
		BaseContext:  func(net.Listener) context.Context { return ctx },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/send" {
				http.Error(w, "不支持此命令", http.StatusNotFound)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+ep.Token)) != 1 {
				http.Error(w, "CLI 身份校验失败，请重启 Snapover", http.StatusUnauthorized)
				return
			}
			if r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "请求格式无效", http.StatusBadRequest)
				return
			}
			select {
			case busy <- struct{}{}:
				defer func() { <-busy }()
			default:
				http.Error(w, "已有 CLI 命令正在发送，请等待完成", http.StatusConflict)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			var request Request
			if err := decoder.Decode(&request); err != nil {
				http.Error(w, "请求内容无效或超过大小限制", http.StatusBadRequest)
				return
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF || request.TimeoutMS <= 0 || request.TimeoutMS > MaxTimeout.Milliseconds() {
				http.Error(w, "请求内容或超时时间无效", http.StatusBadRequest)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), time.Duration(request.TimeoutMS)*time.Millisecond)
			defer cancel()
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.Header().Set("Cache-Control", "no-store")
			encoder := json.NewEncoder(w)
			emit := func(event Event) {
				if err := encoder.Encode(event); err != nil {
					cancel()
				}
				if err := http.NewResponseController(w).Flush(); err != nil {
					cancel()
				}
			}
			emit(Event{State: "started", Message: "正在发送，等待对端确认…"})
			message, err := handler(ctx, request, func(done, total int64) {
				emit(Event{State: "progress", Done: done, Total: total})
			})
			if err != nil {
				emit(Event{State: "error", Message: err.Error()})
			} else {
				emit(Event{State: "complete", Message: message})
			}
		}),
	}
	if err := writeEndpoint(s.path, ep); err != nil {
		ln.Close()
		lock.Close()
		return nil, err
	}
	go func() { _ = s.http.Serve(ln) }()
	go func() { <-ctx.Done(); s.Close() }()
	return s, nil
}

func (s *Server) Close() {
	s.once.Do(func() {
		_ = s.http.Close()
		_ = os.Remove(s.path)
		_ = s.lock.Close()
	})
}

func writeEndpoint(path string, ep endpoint) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".cli-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := protectFile(f.Name()); err != nil {
		f.Close()
		return err
	}
	if err := json.NewEncoder(f).Encode(ep); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func Send(ctx context.Context, dataDir string, request Request, onEvent func(Event)) error {
	f, err := os.Open(filepath.Join(dataDir, "cli.json"))
	if err != nil {
		return errors.New("找不到后台 Snapover，请先启动新版 Snapover；自定义数据目录请使用 --data")
	}
	var ep endpoint
	err = json.NewDecoder(io.LimitReader(f, 4096)).Decode(&ep)
	f.Close()
	host, port, addrErr := net.SplitHostPort(ep.Address)
	portNum, portErr := strconv.Atoi(port)
	if err != nil || addrErr != nil || host != "127.0.0.1" || portErr != nil || portNum < 1 || portNum > 65535 || len(ep.Token) != 64 {
		return errors.New("CLI 服务信息无效，请重启 Snapover")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+ep.Address+"/send", bytes.NewReader(body))
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+ep.Token)
	r.Header.Set("Content-Type", "application/json")
	// Never send the local token or payload to a configured HTTP proxy or redirect.
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(r)
	if err != nil {
		return fmt.Errorf("无法联系后台 Snapover，请检查是否已启动或重启应用：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("CLI 请求失败（%d）：%s", response.StatusCode, bytes.TrimSpace(message))
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	for {
		var event Event
		if err := decoder.Decode(&event); err != nil {
			return fmt.Errorf("未收到发送结果，请检查对端后再重试：%w", err)
		}
		switch event.State {
		case "complete":
			if onEvent != nil {
				onEvent(event)
			}
			return nil
		case "error":
			return errors.New(event.Message)
		default:
			if onEvent != nil {
				onEvent(event)
			}
		}
	}
}
