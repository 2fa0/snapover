package crashlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallNotesUncleanSession(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)

	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, sessionName), []byte("pid=9\nstarted=then\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(buf)
	if !strings.Contains(text, "上次没有正常退出") || !strings.Contains(text, "pid=9") {
		t.Fatalf("missing unclean note:\n%s", text)
	}
	Breadcrumb("开始读取剪贴板\nformats=public.rtf")
	writef("") // barrier: production breadcrumbs deliberately do not wait for disk
	buf, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf), "开始读取剪贴板 formats=public.rtf") {
		t.Fatalf("breadcrumb not flushed:\n%s", buf)
	}

	MarkClean()
	if _, err := os.Stat(filepath.Join(logs, sessionName)); !os.IsNotExist(err) {
		t.Fatalf("session marker still present: %v", err)
	}
}

func TestBreadcrumbBeforeInstall(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)
	Breadcrumb("should not panic")
}

func TestRotateKeepsPreviousFile(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)

	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(logs, fileName)
	if err := os.WriteFile(old, []byte(strings.Repeat("x", rotateAt)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old + ".1"); err != nil {
		t.Fatal(err)
	}
}

func TestBreadcrumbDoesNotWaitForBlockedWriter(t *testing.T) {
	resetForTest()
	blocked := &logWriter{events: make(chan event, 1)}
	mu.Lock()
	sink = blocked
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); sink = nil; mu.Unlock() })
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			Breadcrumb("queue remains bounded")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("breadcrumb waited for storage")
	}
	if len(blocked.events) != 1 {
		t.Fatal("queue exceeded its capacity")
	}
}

func TestCrashLogRotatesWhileRunning(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)
	path, err := Install(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writef("%s", strings.Repeat("x", rotateAt))
	writef("after runtime rotation\n")
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(current), "after runtime rotation") || len(current) > rotateAt {
		t.Fatal("runtime rotation did not bound log size")
	}
}
