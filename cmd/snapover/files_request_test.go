package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	clipsync "github.com/2fa0/snapover/internal/sync"
)

func TestSendToPreservesFileArguments(t *testing.T) {
	wd := t.TempDir()
	abs := filepath.Join(wd, "中文 报告.pdf")
	for _, test := range []struct {
		args      []string
		want      []string
		requested bool
	}{
		{[]string{"--send-file", abs}, []string{abs}, true},
		{[]string{"-send-file=" + abs}, []string{abs}, true},
		{[]string{"--send-file=" + abs}, []string{abs}, true},
		{[]string{"--send-file", "a b.txt", "另一份.txt"}, []string{filepath.Join(wd, "a b.txt"), filepath.Join(wd, "另一份.txt")}, true},
		{[]string{"--send-file"}, nil, true},
		{[]string{"--send-file="}, []string{""}, true},
		{[]string{"--peer", "127.0.0.1"}, nil, false},
		{nil, nil, false},
	} {
		original := append([]string(nil), test.args...)
		got, requested := sendToPaths(test.args, wd)
		if requested != test.requested || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%v: %v %v", test.args, got, requested)
		}
		if !reflect.DeepEqual(original, test.args) {
			t.Fatal("mutated forwarded arguments")
		}
	}
}

func TestFileRequestValidationAndBusyState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "报告 file.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &app{engine: &clipsync.Engine{}}
	if !a.prepareFileRequest([]string{path}) || a.filePath != path || a.fileSize != 5 {
		t.Fatalf("file not selected: %+v", a)
	}
	a.fileBusy = true
	if a.prepareFileRequest([]string{"different"}) || a.filePath != path || a.fileCommandHint == "" {
		t.Fatal("busy request replaced the current file")
	}
	a.fileBusy = false
	for _, paths := range [][]string{{path, path}, {dir}, {filepath.Join(dir, "missing")}, nil} {
		if a.prepareFileRequest(paths) || a.filePath != "" || a.fileMessage == "" {
			t.Fatalf("accepted invalid request %v", paths)
		}
	}
}

func TestFileRequestWaitsForWindowStartup(t *testing.T) {
	a := &app{}
	a.handleFileRequest([]string{"file"}, true)
	if a.pendingFileRequest == nil || !a.pendingFileRequest.cold || a.pendingFileRequest.paths[0] != "file" {
		t.Fatal("lost pre-startup request")
	}
}

func TestColdSendOnlyAcceptsExpectedDevice(t *testing.T) {
	for _, test := range []struct {
		cold             bool
		expected, online string
		want             bool
	}{
		{true, "expected", "expected", true},
		{true, "expected", "different", false},
		{true, "", "different", false},
		{false, "expected", "different", true},
	} {
		if got := canAutoSendFile(test.cold, test.expected, test.online); got != test.want {
			t.Fatalf("auto-send policy %+v: %v", test, got)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if err := waitSendPeer(ctx, "expected", func() (clipsync.Link, bool) { return clipsync.Link{}, false }); err == nil {
		t.Fatal("offline request did not time out")
	}
	ctx = context.Background()
	if err := waitSendPeer(ctx, "expected", func() (clipsync.Link, bool) { return clipsync.Link{ID: "different"}, true }); err == nil {
		t.Fatal("accepted unexpected device")
	}
	if err := waitSendPeer(ctx, "expected", func() (clipsync.Link, bool) { return clipsync.Link{ID: "expected"}, true }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if err := waitSendPeer(ctx, "expected", func() (clipsync.Link, bool) { return clipsync.Link{ID: "expected"}, true }); err == nil {
		t.Fatal("canceled request accepted a peer")
	}
}
