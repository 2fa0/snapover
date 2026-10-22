package crashlog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMaintainRemovesExpiredLogs(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-retainFor - time.Hour).Format(time.RFC3339)
	ordinary := filepath.Join(logs, ordinaryName)
	// Recent mtime must not keep a file whose first record is already expired.
	if err := os.WriteFile(ordinary, []byte("time="+old+" msg=from-last-week\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(ordinary, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"snapover.log.1", "snapover.log.2", "clipbridge.log"} {
		path := filepath.Join(logs, name)
		if err := os.WriteFile(path, []byte("old "+name), 0o600); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-retainFor - time.Hour)
		if err := os.Chtimes(path, past, past); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(logs, sessionName), []byte("pid=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recentBackup := filepath.Join(logs, fileName+".1")
	if err := os.WriteFile(recentBackup, []byte("recent backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	historyLog := filepath.Join(dir, "history", "history.log")
	if err := os.MkdirAll(filepath.Dir(historyLog), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(historyLog, []byte("history journal"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootNote := filepath.Join(dir, "notes.log")
	if err := os.WriteFile(rootNote, []byte("not ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "clipbridge.log")
	if err := os.WriteFile(legacy, []byte("legacy root"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-retainFor - time.Hour)
	if err := os.Chtimes(legacy, past, past); err != nil {
		t.Fatal(err)
	}

	if Maintain(dir) == 0 {
		t.Fatal("expected expired logs to be removed")
	}
	for _, path := range []string{ordinary, ordinary + ".1", filepath.Join(logs, "snapover.log.2"), filepath.Join(logs, "clipbridge.log"), legacy} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expired log still present: %s (%v)", path, err)
		}
	}
	for _, path := range []string{filepath.Join(logs, sessionName), recentBackup, historyLog, rootNote} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("kept file missing: %s (%v)", path, err)
		}
	}
}

func TestMaintainKeepsRecentLog(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logs, ordinaryName)
	body := "time=" + time.Now().Format(time.RFC3339) + " msg=keep\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(logs, "notes.log")
	if err := os.WriteFile(fresh, []byte("fresh unknown"), 0o600); err != nil {
		t.Fatal(err)
	}
	if Maintain(dir) != 0 {
		t.Fatal("recent logs were removed")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != body {
		t.Fatalf("recent log changed: %q %v", data, err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal(err)
	}
}

func TestExtraGenerationRemovedImmediately(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(logs, "snapover.log.3")
	if err := os.WriteFile(extra, []byte("extra"), 0o600); err != nil {
		t.Fatal(err)
	}
	if PruneInactive(dir) != 1 {
		t.Fatal("extra generation was not removed")
	}
	if _, err := os.Stat(extra); !os.IsNotExist(err) {
		t.Fatal("extra generation still present")
	}
}

func TestOversizedBackupIsDiscarded(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logs, ordinaryName+".1")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maxBackupBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if Maintain(dir) == 0 {
		t.Fatal("expected oversized backup to be removed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("oversized backup kept")
	}
}

func TestOrdinaryRotationReplacesPreviousBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), ordinaryName)
	if err := os.WriteFile(path+".1", []byte("stale-backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("a"), rotateAt), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenOrdinary(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(current, []byte("aaa")) || !bytes.Contains(current, []byte("after\n")) {
		t.Fatalf("current log = %q", current)
	}
	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(backup, []byte("stale-backup")) || !bytes.Contains(backup, []byte("a")) {
		t.Fatal("previous backup was not replaced")
	}
}

func TestOrdinaryWriterDropsAgedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ordinaryName)
	if err := os.WriteFile(path, []byte("time="+time.Now().Format(time.RFC3339)+" msg=old-session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenOrdinary(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.mu.Lock()
	f.started = time.Now().Add(-retainFor - time.Hour)
	f.mu.Unlock()
	if _, err := f.Write([]byte("fresh\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "old-session") || !strings.Contains(string(data), "fresh") {
		t.Fatalf("writer did not age-rotate: %s", data)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("aged backup kept: %v", err)
	}
}

func TestInstallDropsExpiredCrashLog(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)

	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-retainFor - time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(logs, fileName), []byte(old+" ancient crash\n"), 0o600); err != nil {
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
	if strings.Contains(string(buf), "ancient") {
		t.Fatalf("expired crash log was kept:\n%s", buf)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("expired crash backup kept: %v", err)
	}
}
