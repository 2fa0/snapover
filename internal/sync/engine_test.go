package sync

import (
	"bytes"
	"context"
	"crypto/tls"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/wire"
)

func TestKeepTieBreak(t *testing.T) {
	if !keep("a", "b", true) || keep("a", "b", false) {
		t.Fatal("smaller id should be the dialer")
	}
	if keep("b", "a", true) || !keep("b", "a", false) {
		t.Fatal("larger id should accept, not dial")
	}
}

func TestLocalSendPolicy(t *testing.T) {
	item := mustText(t, "hi")
	if send, msg := localSend(policy{}, item, true); !send || msg != "" {
		t.Fatalf("send=%v %s", send, msg)
	}
	if send, _ := localSend(policy{Paused: true}, item, true); send {
		t.Fatal("paused should not send")
	}
	img := mustPNG(t)
	if send, _ := localSend(policy{TextOnly: true}, img, true); send {
		t.Fatal("text-only should keep images local")
	}
	if send, _ := localSend(policy{MaxImage: 1}, img, true); send {
		t.Fatal("over the image cap should stay local")
	}
}

func TestLocalCopyNotifiesWithoutPeer(t *testing.T) {
	e := testEngine(t, &memBoard{})
	var got []Notice
	e.OnNotice = func(n Notice) error { got = append(got, n); return nil }
	e.HandleLocal(1, mustText(t, "hi"))
	if len(got) != 1 || got[0].Kind != NoticeLocal || got[0].Item.Text != "hi" {
		t.Fatalf("%+v", got)
	}
	if e.Connected() {
		t.Fatal("should not be connected")
	}
}

func TestLocalTextLimitSkipsHistoryAndCanBeRaised(t *testing.T) {
	e := testEngine(t, &memBoard{})
	item := mustText(t, strings.Repeat("a", clip.DefaultMaxTextBytes+1))
	var got []Notice
	e.OnNotice = func(n Notice) error { got = append(got, n); return nil }
	e.HandleLocal(1, item)
	if len(got) != 1 || got[0].Kind != NoticeStatus || got[0].Item.Kind != "" {
		t.Fatal("oversize text was offered to history")
	}
	e.SetTextLimit(2 << 20)
	e.HandleLocal(2, item)
	if len(got) != 2 || got[1].Kind != NoticeLocal || got[1].Item.Text != item.Text {
		t.Fatal("raised limit did not capture the same text")
	}
}

func TestLocalCopyWins(t *testing.T) {
	board := &memBoard{}
	e := testEngine(t, board)
	e.localActive = time.Now()
	e.deliver(mustText(t, "from-peer"))
	if n := len(board.Writes()); n != 0 {
		t.Fatalf("overwrote local clipboard, writes=%d", n)
	}
}

func TestEchoDoesNotResend(t *testing.T) {
	board := &memBoard{}
	e := testEngine(t, board)
	item := mustText(t, "echo")
	e.deliver(item)
	seq, err := board.Sequence()
	if err != nil {
		t.Fatal(err)
	}
	e.HandleLocal(seq, item)
	if e.haveLast {
		t.Fatal("echo was treated as a local copy")
	}
}

func TestRemoteWriteAllowsRecopyOfPreviousLocalContent(t *testing.T) {
	board := &memBoard{}
	e := testEngine(t, board)
	e.sess = newSession(nil, "peer", true, discardLog(), nil)
	local := mustText(t, "A")
	notices := 0
	e.OnNotice = func(n Notice) error {
		if n.Kind == NoticeLocal && n.Item.Text == "A" {
			notices++
		}
		return nil
	}
	e.HandleLocal(1, local)
	if len(e.sess.out) != 1 {
		t.Fatal("first copy was not queued")
	}
	<-e.sess.out
	e.mu.Lock()
	e.localActive = time.Time{}
	e.mu.Unlock()
	if err := e.deliverFrom(nil, mustText(t, "B")); err != nil {
		t.Fatal(err)
	}
	e.HandleLocal(2, local)
	if notices != 2 || len(e.sess.out) != 1 {
		t.Fatalf("recopy after remote write was dropped: notices=%d queued=%d", notices, len(e.sess.out))
	}
}

func TestRecopyOfRecentRemoteContentIsNotAnEcho(t *testing.T) {
	board := &memBoard{}
	e := testEngine(t, board)
	e.sess = newSession(nil, "peer", true, discardLog(), nil)
	remote := mustText(t, "A")
	if err := e.deliverFrom(nil, remote); err != nil {
		t.Fatal(err)
	}
	owned, err := board.Sequence()
	if err != nil {
		t.Fatal(err)
	}
	e.HandleLocal(owned, remote)
	if e.haveLast {
		t.Fatal("owned generation became a local copy")
	}
	e.HandleLocal(owned+1, mustText(t, "B"))
	e.HandleLocal(owned+2, remote)
	if len(e.sess.out) != 2 {
		t.Fatalf("fresh copies queued=%d, want 2", len(e.sess.out))
	}
}

func TestExchangeTextAndImage(t *testing.T) {
	a, b := newPair(t)
	item := mustText(t, "跨设备 你好")
	item.SourceApp = "微信"
	b.HandleLocal(1, item)
	waitWrite(t, a.Board.(*memBoard), item.Hash)
	if got := a.Board.(*memBoard).Writes(); got[len(got)-1].SourceApp != "微信" {
		t.Fatalf("text source = %q", got[len(got)-1].SourceApp)
	}

	// B just copied locally, so the 2s local-priority window would correctly
	// refuse the image. Clear it; that rule has its own test.
	b.mu.Lock()
	b.localActive = time.Time{}
	b.mu.Unlock()
	img := mustPNG(t)
	img.SourceApp = "截图"
	a.HandleLocal(2, img)
	waitWrite(t, b.Board.(*memBoard), img.Hash)
	if got := b.Board.(*memBoard).Writes(); got[len(got)-1].SourceApp != "截图" {
		t.Fatalf("image source = %q", got[len(got)-1].SourceApp)
	}
}

func TestReconnectUsingSavedAddress(t *testing.T) {
	left, right := newIdentities(t)
	a := engineFor(t, left, &memBoard{})
	b := engineFor(t, right, &memBoard{})
	b.ListenPort = "49001"
	a.SetPairing(true)
	b.SetPairing(true)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", a.TLS)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, a.ListenPort, _ = net.SplitHostPort(ln.Addr().String())
	addr := ln.Addr().String()
	for round, text := range []string{"paired", "reconnected"} {
		accepted := make(chan struct{})
		go func() {
			defer close(accepted)
			conn, err := ln.Accept()
			if err == nil {
				_ = a.Attach(conn.(*tls.Conn), false)
			}
		}()
		ctx, cancel := context.WithCancel(context.Background())
		dialed := make(chan struct{})
		client := b
		go func() {
			defer close(dialed)
			Dial(ctx, addr, client)
		}()
		stop := func() {
			cancel()
			for _, e := range []*Engine{a, client} {
				e.mu.Lock()
				sess := e.sess
				e.mu.Unlock()
				if sess != nil {
					sess.Close()
				}
			}
		}
		t.Cleanup(stop)
		deadline := time.Now().Add(3 * time.Second)
		for !a.Connected() || !client.Connected() {
			approvePair(t, a, client)
			if time.Now().After(deadline) {
				t.Fatal("saved address did not reconnect")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if got := a.Peers.List()[0].Peer.Addr; got != "127.0.0.1:49001" {
			t.Fatalf("incoming peer address=%q", got)
		}
		item := mustText(t, text)
		client.HandleLocal(1, item)
		waitWrite(t, a.Board.(*memBoard), item.Hash)
		stop()
		for _, done := range []chan struct{}{accepted, dialed} {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("connection did not stop")
			}
		}
		if round == 1 {
			break
		}
		// Reopen the client's trust file, as a fresh process would.
		store, err := OpenStore(filepath.Dir(client.Peers.path))
		if err != nil {
			t.Fatal(err)
		}
		b = engineFor(t, right, &memBoard{})
		b.Peers = store
		b.ListenPort = "49001"
		addr = store.List()[0].Peer.Addr
	}
}

func TestRejectChangedFingerprint(t *testing.T) {
	left, right := newIdentities(t)
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Allow(right.DeviceID, "00", "old", "macos", ""); err != nil {
		t.Fatal(err)
	}
	board := &memBoard{}
	e := &Engine{
		LocalID: left.DeviceID, Name: "L", Platform: "macos", Fingerprint: left.Fingerprint,
		TLS: identity.TLSConfig(left), Peers: store, Board: board,
		Guard: clip.NewEchoGuard(5 * time.Second), Log: discardLog(),
	}
	client, server := handshakePair(t, left, right)
	defer client.Close()
	go func() {
		_ = wire.WriteMsg(client, wire.TypeHello, wire.Hello{
			DeviceID: right.DeviceID, Name: "R", Platform: "macos", Proto: wire.Proto, Fingerprint: right.Fingerprint,
		})
		_, _, _ = wire.ReadFrame(client)
	}()
	err = e.Attach(server, false)
	if err == nil {
		t.Fatal("changed fingerprint was accepted")
	}
	client.Close()
}

func newPair(t *testing.T) (*Engine, *Engine) {
	t.Helper()
	left, right := newIdentities(t)
	aBoard, bBoard := &memBoard{}, &memBoard{}
	a := engineFor(t, left, aBoard)
	b := engineFor(t, right, bBoard)
	a.SetPairing(true)
	b.SetPairing(true)
	client, server := handshakePair(t, left, right)
	done := make(chan struct{})
	go func() {
		_ = a.Attach(server, false)
		close(done)
	}()
	go func() { _ = b.Attach(client, true) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		approvePair(t, a, b)
		if a.Connected() && b.Connected() {
			t.Cleanup(func() {
				client.Close()
				server.Close()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
				}
			})
			return a, b
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("peers did not connect")
	return nil, nil
}

func engineFor(t *testing.T, id *identity.Identity, board clip.Board) *Engine {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Engine{
		LocalID: id.DeviceID, Name: "test", Platform: "macos", Fingerprint: id.Fingerprint,
		TLS: identity.TLSConfig(id), Peers: store, Board: board,
		Guard: clip.NewEchoGuard(5 * time.Second), Log: discardLog(),
	}
}

func newIdentities(t *testing.T) (*identity.Identity, *identity.Identity) {
	t.Helper()
	a, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func handshakePair(t *testing.T, left, right *identity.Identity) (*tls.Conn, *tls.Conn) {
	t.Helper()
	rawA, rawB := net.Pipe()
	server := tls.Server(rawA, identity.TLSConfig(left))
	client := tls.Client(rawB, identity.TLSConfig(right))
	errCh := make(chan error, 1)
	go func() { errCh <- server.Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	return client, server
}

func testEngine(t *testing.T, board clip.Board) *Engine {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Engine{
		LocalID: "aa", Name: "t", Platform: "macos",
		Peers: store, Board: board, Guard: clip.NewEchoGuard(5 * time.Second), Log: discardLog(),
	}
}

func waitWrite(t *testing.T, board *memBoard, hash [32]byte) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, item := range board.Writes() {
			if item.Hash == hash {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for hash %x", hash[:4])
}

func mustText(t *testing.T, s string) clip.Item {
	t.Helper()
	item, err := clip.FromText(s)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func mustPNG(t *testing.T) clip.Item {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	img.SetNRGBA(2, 1, color.NRGBA{R: 1, G: 2, B: 3, A: 64})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromPNG(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type memBoard struct {
	mu     sync.Mutex
	seq    uint64
	item   clip.Item
	writes []clip.Item
}

func (m *memBoard) Sequence() (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seq, nil
}
func (m *memBoard) Snapshot() (clip.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.item, nil
}
func (m *memBoard) Write(item clip.Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.item = item
	m.seq++
	m.writes = append(m.writes, item)
	return nil
}
func (m *memBoard) Writes() []clip.Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]clip.Item, len(m.writes))
	copy(out, m.writes)
	return out
}

func approvePair(t *testing.T, a, b *Engine) {
	t.Helper()
	left, lok := a.PendingPair()
	right, rok := b.PendingPair()
	if lok && rok {
		if left.Code != right.Code {
			t.Fatalf("pair codes differ: %s, %s", left.Code, right.Code)
		}
		a.ConfirmPair(left.Token, true)
		b.ConfirmPair(right.Token, true)
	}
}
