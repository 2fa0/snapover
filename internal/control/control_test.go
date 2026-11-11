package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalCommandStreamsAndReportsResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	s, err := start(ctx, dir, func(ctx context.Context, request Request, progress func(int64, int64)) (string, error) {
		if request.Value == "error" {
			return "", errors.New("对端拒绝")
		}
		if request.Value == "timeout" {
			<-ctx.Done()
			return "", ctx.Err()
		}
		progress(1, 2)
		return request.Value, nil
	}, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var events []Event
	err = Send(ctx, dir, Request{Kind: "text", Value: "中文\n", TimeoutMS: 1000}, func(event Event) { events = append(events, event) })
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[1].Done != 1 || events[2].State != "complete" || events[2].Message != "中文\n" {
		t.Fatalf("%+v", events)
	}
	if err := Send(ctx, dir, Request{Value: "error", TimeoutMS: 1000}, nil); err == nil || !strings.Contains(err.Error(), "对端拒绝") {
		t.Fatalf("%v", err)
	}
	if err := Send(ctx, dir, Request{Value: "timeout", TimeoutMS: 10}, nil); err == nil {
		t.Fatal("timeout reported success")
	}
	if other, err := Start(ctx, dir, nil); err == nil {
		other.Close()
		t.Fatal("another server replaced active endpoint")
	}
	s.Close()
	if _, err := os.Stat(filepath.Join(dir, "cli.json")); !os.IsNotExist(err) {
		t.Fatalf("endpoint retained: %v", err)
	}
	other, err := Start(ctx, dir, func(context.Context, Request, func(int64, int64)) (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
}

func TestLocalAuthenticationAndInvalidRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	called := false
	s, err := Start(ctx, dir, func(context.Context, Request, func(int64, int64)) (string, error) { called = true; return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data, err := os.ReadFile(filepath.Join(dir, "cli.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ep endpoint
	if err := json.Unmarshal(data, &ep); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{}}
	for _, test := range []struct {
		token, body string
		status      int
	}{
		{"", `{"timeout_ms":1000}`, http.StatusUnauthorized},
		{"wrong", `{"timeout_ms":1000}`, http.StatusUnauthorized},
		{ep.Token, `{"timeout_ms":-1}`, http.StatusBadRequest},
		{ep.Token, `{"timeout_ms":1000} {}`, http.StatusBadRequest},
		{ep.Token, `{"timeout_ms":1000,"unknown":true}`, http.StatusBadRequest},
	} {
		r, _ := http.NewRequest(http.MethodPost, "http://"+ep.Address+"/send", strings.NewReader(test.body))
		r.Header.Set("Authorization", "Bearer "+test.token)
		r.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != test.status {
			t.Fatalf("got %d want %d", resp.StatusCode, test.status)
		}
	}
	if called {
		t.Fatal("unauthorized or invalid request invoked handler")
	}
}

func TestEndpointRejectsRemoteAddresses(t *testing.T) {
	dir := t.TempDir()
	if err := writeEndpoint(filepath.Join(dir, "cli.json"), endpoint{Address: "192.168.1.2:1234", Token: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	if err := Send(context.Background(), dir, Request{}, nil); err == nil || !strings.Contains(err.Error(), "服务信息无效") {
		t.Fatalf("%v", err)
	}
}

func TestClientDisconnectCancelsRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	started, stopped := make(chan struct{}), make(chan struct{})
	s, err := Start(ctx, dir, func(ctx context.Context, _ Request, _ func(int64, int64)) (string, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return "", ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	clientCtx, clientCancel := context.WithCancel(ctx)
	defer clientCancel()
	result := make(chan error, 1)
	go func() { result <- Send(clientCtx, dir, Request{TimeoutMS: 1000}, nil) }()
	<-started
	clientCancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("command survived CLI cancellation")
	}
	if err := <-result; err == nil {
		t.Fatal("cancellation reported success")
	}
}

func TestCloseCancelsServerContext(t *testing.T) {
	s, err := Start(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := s.http.BaseContext(nil)
	s.Close()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("closing the server retained its context and cancellation watcher")
	}
}

func TestFileBatchRequestCarriesPaths(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	s, err := Start(ctx, dir, func(_ context.Context, request Request, _ func(int64, int64)) (string, error) {
		return strings.Join(request.Paths, "|"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var events []Event
	request := Request{Kind: "file", Paths: []string{"/a b/一.txt", "/c.txt"}, TimeoutMS: 1000}
	if err := Send(ctx, dir, request, func(event Event) { events = append(events, event) }); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[len(events)-1].Message != "/a b/一.txt|/c.txt" {
		t.Fatalf("%+v", events)
	}
	tooMany := Request{Kind: "file", Paths: make([]string, MaxRequestPaths+1), TimeoutMS: 1000}
	if err := Send(ctx, dir, tooMany, nil); err == nil || !strings.Contains(err.Error(), "文件数量超过上限") {
		t.Fatalf("oversize batch: %v", err)
	}
	// A single payload keeps working for text and image commands.
	if err := Send(ctx, dir, Request{Kind: "text", Value: "你好", TimeoutMS: 1000}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestIdleTimeoutTracksActualProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dir := t.TempDir()
	s, err := start(ctx, dir, func(ctx context.Context, request Request, progress func(int64, int64)) (string, error) {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for done := int64(1); done <= 12; done++ {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-ticker.C:
			}
			if request.Value == "stalled" {
				progress(1, 12)
			} else {
				progress(done, 12)
			}
		}
		return "saved", nil
	}, 150*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// The legacy total timeout is ignored; ongoing progress survives several idle intervals.
	if err = Send(ctx, dir, Request{Value: "moving", TimeoutMS: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err = Send(ctx, dir, Request{Value: "stalled"}, nil); err == nil || !strings.Contains(err.Error(), "没有传输进展") {
		t.Fatalf("duplicate progress kept transfer alive: %v", err)
	}
}

func TestClientIdleTimeoutCancelsUnresponsiveHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dir := t.TempDir()
	stopped := make(chan struct{})
	s, err := start(ctx, dir, func(ctx context.Context, _ Request, _ func(int64, int64)) (string, error) {
		<-ctx.Done()
		close(stopped)
		return "", ctx.Err()
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = send(ctx, dir, Request{}, nil, 100*time.Millisecond); !errors.Is(err, errNoProgress) {
		t.Fatalf("client stalled: %v", err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("client idle timeout did not cancel handler")
	}
}

func TestLongProgressStreamHasNoCumulativeSizeLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	const count = 25000
	s, err := Start(ctx, dir, func(_ context.Context, _ Request, progress func(int64, int64)) (string, error) {
		for i := int64(1); i <= count; i++ {
			progress(i, count)
		}
		return "saved", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var received int
	if err = Send(ctx, dir, Request{}, func(event Event) {
		if event.State == "progress" {
			received++
		}
	}); err != nil {
		t.Fatal(err)
	}
	if received != count {
		t.Fatalf("progress stream truncated at %d", received)
	}
}
