package sync

import (
	"bytes"
	"crypto/tls"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/2fa0/clipbridge/internal/clip"
	"github.com/2fa0/clipbridge/internal/identity"
	"github.com/2fa0/clipbridge/internal/wire"
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
	if send, msg := localSend(policy{}, item, true); !send || msg != "已发送" {
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
	e.OnNotice = func(n Notice) { got = append(got, n) }
	e.HandleLocal(1, mustText(t, "hi"))
	if len(got) != 1 || got[0].Kind != NoticeLocal || got[0].Item.Text != "hi" {
		t.Fatalf("%+v", got)
	}
	if e.Connected() {
		t.Fatal("should not be connected")
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
	e.HandleLocal(9, item)
	if e.haveLast {
		t.Fatal("echo was treated as a local copy")
	}
}

func TestExchangeTextAndImage(t *testing.T) {
	a, b := newPair(t)
	item := mustText(t, "跨设备 你好")
	b.HandleLocal(1, item)
	waitWrite(t, a.Board.(*memBoard), item.Hash)

	// B just copied locally, so the 2s local-priority window would correctly
	// refuse the image. Clear it; that rule has its own test.
	b.mu.Lock()
	b.localActive = time.Time{}
	b.mu.Unlock()
	img := mustPNG(t)
	a.HandleLocal(2, img)
	waitWrite(t, b.Board.(*memBoard), img.Hash)
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
	client, server := handshakePair(t, left, right)
	done := make(chan struct{})
	go func() {
		_ = a.Attach(server, false)
		close(done)
	}()
	go func() { _ = b.Attach(client, true) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
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
