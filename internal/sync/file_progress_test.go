package sync

import (
	"errors"
	"testing"
)

func TestFileClipboardTitle(t *testing.T) {
	if got := fileClipboardTitle(nil); got != "文件" {
		t.Fatalf("empty: %q", got)
	}
	if got := fileClipboardTitle([]string{"/tmp/report.pdf"}); got != "report.pdf" {
		t.Fatalf("one file: %q", got)
	}
	if got := fileClipboardTitle([]string{"/tmp/a.txt", "/tmp/b.txt"}); got != "a.txt 等 2 项" {
		t.Fatalf("batch: %q", got)
	}
}

func TestSupersededFileClipboardStopsBeforePacking(t *testing.T) {
	s := newSession(nil, "peer", true, discardLog(), nil)
	s.fileClipboardSupported = true
	s.fileClipboardCheck = func(queuedItem) error { return errFileClipboardSuperseded }
	var states []string
	s.onFile = func(n Notice) { states = append(states, n.FileState) }
	if err := s.sendFileClipboard(queuedItem{}); !errors.Is(err, errFileClipboardSuperseded) {
		t.Fatalf("superseded copy: %v", err)
	}
	if len(states) != 1 || states[0] != "cancelled" {
		t.Fatalf("a canceled copy started packing: %v", states)
	}
}
