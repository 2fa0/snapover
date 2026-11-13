package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/netproxy"
)

// How long the updater waits on the network. Its own requests have no
// deadline above the socket: a connection that the network abandoned would
// leave a check, or a download started in the background, waiting until the
// system gives up on it, with no window to cancel it and no further check
// scheduled until it ends. These turn that wait into a failure.
const (
	// updateManifestTimeout covers the whole manifest request, retries and
	// all: it is a small JSON file.
	updateManifestTimeout = 30 * time.Second
	// updateStallTimeout is how long a release file may send nothing before
	// the download is abandoned: slow is fine, stopped is not.
	updateStallTimeout = 60 * time.Second
)

// updateRetries are how long updateGet waits before asking again, once, for a
// file that a gateway failed to serve for now.
var updateRetries = []time.Duration{time.Second, 3 * time.Second}

// updateRepo is the releases repository of mygo.json's updates.github, which
// `go tool mygo build` compiles the feed from; a test keeps the two in step.
const updateRepo = "2fa0/snapover"

// updateFeed returns the manifest of this build's target, as the updater's
// own feed has it: update-<target>.json in the latest release of updateRepo.
func updateFeed() string {
	name := "update-" + runtime.GOOS + "-" + runtime.GOARCH + ".json"
	return "https://github.com/" + updateRepo + "/releases/latest/download/" + url.PathEscape(name)
}

// updateSource fetches the manifest and the release files with deadlines, in
// place of the updater's own requests (mygo.Updater.SetSource). What it
// fetches is still the updater's to verify: an update that is not signed with
// the app's key is not installed, wherever the bytes came from.
type updateSource struct{ feed string }

func newUpdateSource() *updateSource { return &updateSource{feed: updateFeed()} }

// Manifest returns the manifest of the running platform. The app offers no
// prereleases, so the feed is the manifest of the latest release whichever
// channel the updater asks for.
func (s *updateSource) Manifest(ctx context.Context, prereleases bool) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, updateManifestTimeout)
	body, err := updateGet(ctx, s.feed)
	if err != nil {
		cancel()
		if statusOf(err) == http.StatusNotFound {
			// The feed this build works out is not the one it was built with:
			// keep updating through the updater's own feed rather than fail
			// every check from now on.
			mygo.Updater.SetSource(nil)
		}
		return nil, err
	}
	return &closingBody{ReadCloser: body, done: cancel}, nil
}

// Open returns the release file that the manifest names, which the updater
// reads until it has the size the manifest gives.
func (s *updateSource) Open(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	body, err := updateGet(ctx, rawURL)
	if err != nil {
		cancel()
		return nil, err
	}
	return newStallGuard(body, cancel, updateStallTimeout), nil
}

// updateGet fetches rawURL through the transport the updater uses, so that
// the system's proxy settings apply to it, and over HTTPS only, as the
// updater insists.
func updateGet(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil, fmt.Errorf("refusing to download %s: updates need HTTPS", rawURL)
	}
	client := &http.Client{Transport: netproxy.Transport()}
	for try := 0; ; try++ {
		body, err := updateTry(ctx, client, rawURL)
		if err == nil {
			return body, nil
		}
		if try >= len(updateRetries) || ctx.Err() != nil || !retryableUpdateError(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(updateRetries[try]):
		}
	}
}

// updateTry asks for rawURL once.
func updateTry(ctx context.Context, client *http.Client, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	// Not the app's name: the store GitHub serves release files from answers
	// 400 to a User-Agent holding bytes outside ASCII.
	req.Header.Set("User-Agent", "mygo-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &statusError{url: rawURL, status: resp.StatusCode}
	}
	return resp.Body, nil
}

// statusError is a response that is not 200.
type statusError struct {
	url    string
	status int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("GET %s: %d %s", e.url, e.status, http.StatusText(e.status))
}

// statusOf returns the status of a response that was not 200, or 0.
func statusOf(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.status
	}
	return 0
}

// retryableUpdateError reports whether err may succeed when asked again: a
// server or gateway failing for now, or a connection that broke while nothing
// had given up yet.
func retryableUpdateError(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		switch se.status {
		case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// closingBody is a body that ends the request it came from when it closes,
// so that a check that stops reading does not keep a connection.
type closingBody struct {
	io.ReadCloser
	done context.CancelFunc
}

func (b *closingBody) Close() error {
	err := b.ReadCloser.Close()
	b.done()
	return err
}

// stallGuard abandons a download that stops arriving. A slow download is
// fine: only a body that sends nothing for d is canceled, which is what a
// connection the network dropped looks like — reads that never return.
type stallGuard struct {
	rc     io.ReadCloser
	cancel context.CancelFunc
	d      time.Duration
	// last is when the last byte arrived, in Unix nanoseconds, and stalled
	// is set once cancel was called, so that the error can say why.
	last    atomic.Int64
	stalled atomic.Bool
	done    atomic.Bool
	timer   *time.Timer
}

func newStallGuard(rc io.ReadCloser, cancel context.CancelFunc, d time.Duration) *stallGuard {
	g := &stallGuard{rc: rc, cancel: cancel, d: d}
	g.last.Store(time.Now().UnixNano())
	g.timer = time.AfterFunc(g.tick(), g.check)
	return g
}

// tick is how long the guard waits before looking at the progress again:
// often enough to give up soon after a download stops, rarely enough to cost
// nothing while it runs.
func (g *stallGuard) tick() time.Duration { return max(g.d/4, 10*time.Millisecond) }

// check cancels the request when nothing arrived for d.
func (g *stallGuard) check() {
	if g.done.Load() {
		return
	}
	if time.Since(time.Unix(0, g.last.Load())) >= g.d {
		g.stalled.Store(true)
		g.cancel()
		return
	}
	g.timer.Reset(g.tick())
}

func (g *stallGuard) Read(p []byte) (int, error) {
	n, err := g.rc.Read(p)
	if n > 0 {
		g.last.Store(time.Now().UnixNano())
	}
	if err != nil && g.stalled.Load() {
		return n, errors.New("the download stalled: the connection sent nothing")
	}
	return n, err
}

func (g *stallGuard) Close() error {
	g.done.Store(true)
	g.timer.Stop()
	g.cancel()
	return g.rc.Close()
}
