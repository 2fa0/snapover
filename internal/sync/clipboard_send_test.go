package sync

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

func TestSendClipboardConfirmsWithoutChangingLocalBoard(t *testing.T) {
	a, b := newPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, item := range []clip.Item{mustText(t, "CLI\r\n文字\n"), mustPNG(t)} {
		item.SourceApp = "Snapover CLI"
		if err := a.SendClipboard(ctx, b.LocalID, item); err != nil {
			t.Fatal(err)
		}
		writes := b.Board.(*memBoard).Writes()
		if len(writes) == 0 || writes[len(writes)-1].Hash != item.Hash || writes[len(writes)-1].SourceApp != item.SourceApp {
			t.Fatal("success was reported without a matching remote write")
		}
		// Explicit requests should resend even an identical item.
		if err := a.SendClipboard(ctx, b.LocalID, item); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.Board.(*memBoard).Writes()) != 0 {
		t.Fatal("changed local clipboard")
	}
	if len(b.Board.(*memBoard).Writes()) != 4 {
		t.Fatal("explicit resend was deduplicated")
	}
	if err := a.SendClipboard(ctx, "another-device", mustText(t, "private")); err == nil {
		t.Fatal("wrong peer accepted")
	}
}

func TestSendClipboardWorksInManualMode(t *testing.T) {
	a, b := newPair(t)
	a.SetManualSync(true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, item := range []clip.Item{mustText(t, "explicit manual send"), mustPNG(t)} {
		if err := a.SendClipboard(ctx, b.LocalID, item); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.Board.(*memBoard).Writes()) != 2 {
		t.Fatal("manual mode lost an explicit send")
	}
}

func TestQueuedCLIReceivesLocalPolicyRejection(t *testing.T) {
	for _, policy := range []string{"pause", "privacy"} {
		t.Run(policy, func(t *testing.T) {
			left, right := net.Pipe()
			defer right.Close()
			s := newSession(tls.Client(left, &tls.Config{}), "peer", true, discardLog(), nil)
			defer s.Close()
			s.clipboardResultSupported = true
			e := testEngine(t, &memBoard{})
			e.adopt(s, Link{ID: "peer"})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- e.SendClipboard(ctx, "peer", mustText(t, "queued")) }()
			await(t, func() bool { return len(s.out) == 1 })
			if policy == "pause" {
				e.SetPolicy(true, false, 0)
			} else {
				e.SetPrivacyPolicy(true, nil)
			}
			select {
			case err := <-result:
				if err == nil || errors.Is(err, context.DeadlineExceeded) || s.isClosed() {
					t.Fatalf("local rejection did not preserve the connection: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("CLI kept waiting after its queued request was rejected")
			}
		})
	}
}

func TestLargeTextUsesConfiguredLimitsWithoutReconnect(t *testing.T) {
	a, b := newPair(t)
	item := mustText(t, strings.Repeat("a", clip.MaxTextBytes))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.SendClipboard(ctx, b.LocalID, item); err == nil || !strings.Contains(err.Error(), "1 MB") {
		t.Fatalf("default sender limit was ignored: %v", err)
	}
	a.SetTextLimit(clip.MaxTextBytes)
	if err := a.SendClipboard(ctx, b.LocalID, item); err == nil || !strings.Contains(err.Error(), "对端未接收") {
		t.Fatalf("default receiver limit was ignored: %v", err)
	}
	if len(b.Board.(*memBoard).Writes()) != 0 {
		t.Fatal("receiver wrote text above its limit")
	}
	b.SetTextLimit(clip.MaxTextBytes)
	if err := a.SendClipboard(ctx, b.LocalID, item); err != nil {
		t.Fatal(err)
	}
	writes := b.Board.(*memBoard).Writes()
	if len(writes) != 1 || writes[0].Text != item.Text {
		t.Fatal("long text was lost or truncated in transport")
	}
	b.SetTextLimit(clip.DefaultMaxTextBytes)
	if err := a.SendClipboard(ctx, b.LocalID, item); err == nil {
		t.Fatal("lowered limit did not take effect")
	}
	if err := a.SendClipboard(ctx, b.LocalID, mustText(t, "still connected")); err != nil {
		t.Fatal(err)
	}
}

func TestSendClipboardReportsRemoteRejections(t *testing.T) {
	for _, reason := range []string{"paused", "recent-copy", "write-failed"} {
		t.Run(reason, func(t *testing.T) {
			a, b := newPair(t)
			b.mu.Lock()
			switch reason {
			case "paused":
				b.paused = true
			case "recent-copy":
				b.localActive = time.Now()
			case "write-failed":
				b.Board = &failedBoard{memBoard: b.Board.(*memBoard)}
			}
			b.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := a.SendClipboard(ctx, b.LocalID, mustText(t, "must not succeed")); err == nil {
				t.Fatal("rejection reported as success")
			}
			b.mu.Lock()
			b.paused, b.localActive = false, time.Time{}
			if failed, ok := b.Board.(*failedBoard); ok {
				b.Board = failed.memBoard
			}
			b.mu.Unlock()
			if err := a.SendClipboard(ctx, b.LocalID, mustText(t, "still connected")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type failedBoard struct{ *memBoard }

func (b *failedBoard) Write(clip.Item) error { return errors.New("clipboard busy") }

func TestSendClipboardRejectsLegacyAndLocalPolicy(t *testing.T) {
	a, b := newPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a.SetPolicy(true, false, 0)
	if err := a.SendClipboard(ctx, b.LocalID, mustText(t, "paused")); err == nil {
		t.Fatal("sent while paused")
	}
	a.SetPolicy(false, true, 0)
	if err := a.SendClipboard(ctx, b.LocalID, mustPNG(t)); err == nil {
		t.Fatal("sent image with text-only policy")
	}
	a.SetPolicy(false, false, 1)
	if err := a.SendClipboard(ctx, b.LocalID, mustPNG(t)); err == nil {
		t.Fatal("ignored image limit")
	}
	a.SetPolicy(false, false, 0)
	a.mu.Lock()
	a.sess.clipboardResultSupported = false
	a.mu.Unlock()
	if err := a.SendClipboard(ctx, b.LocalID, mustText(t, "legacy")); err == nil || !strings.Contains(err.Error(), "更新") {
		t.Fatalf("%v", err)
	}
	if len(b.Board.(*memBoard).Writes()) != 0 {
		t.Fatal("rejected request was delivered")
	}
}

func TestClipboardTimeoutDropsQueuedPayload(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	s := newSession(tls.Client(left, &tls.Config{}), "peer", true, discardLog(), nil)
	s.clipboardResultSupported = true
	e := testEngine(t, &memBoard{})
	e.adopt(s, Link{ID: "peer"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := e.SendClipboard(ctx, "peer", mustText(t, "queued")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
	if len(s.out) != 0 || s.queuedBytes != 0 || !s.closed {
		t.Fatal("timed out command retained data for later delivery")
	}
	select {
	case <-s.done:
	default:
		t.Fatal("transport was not canceled")
	}
}

func TestSimultaneousClipboardImages(t *testing.T) {
	a, b := newPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := make(chan error, 2)
	left, right := mustPNG(t), mustPNG(t)
	go func() { results <- a.SendClipboard(ctx, b.LocalID, left) }()
	go func() { results <- b.SendClipboard(ctx, a.LocalID, right) }()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}
