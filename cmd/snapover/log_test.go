package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/crashlog"
)

func TestLoggerWritesFileWithoutConsole(t *testing.T) {
	if dir := os.Getenv("SNAPOVER_TEST_LOG_DIR"); dir != "" {
		if _, err := crashlog.Install(dir); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("SNAPOVER_TEST_LOG_BLOCKED") == "1" {
			if err := os.Mkdir(filepath.Join(dir, "logs", "snapover.log"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		// A Windows GUI process may have no usable stdout. Isolate the closed
		// handle and logger lifetime from the parent test process.
		if err := os.Stdout.Close(); err != nil {
			t.Fatal(err)
		}
		log := newLogger(dir, slog.LevelInfo)
		log.Info("startup without console")
		log.Warn("keepalive without console")
		return
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprint(blocked), func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(exe, "-test.run=^TestLoggerWritesFileWithoutConsole$")
			cmd.Env = append(os.Environ(), "SNAPOVER_TEST_LOG_DIR="+dir, "SNAPOVER_TEST_UI=0", "SNAPOVER_TEST_LOG_BLOCKED=0")
			if blocked {
				cmd.Env = append(cmd.Env, "SNAPOVER_TEST_LOG_BLOCKED=1")
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("logger subprocess: %v\n%s", err, out)
			}
			data, err := os.ReadFile(filepath.Join(dir, "logs", "crash.log"))
			if err != nil {
				t.Fatal(err)
			}
			marker := "普通日志写入已验证"
			if blocked {
				marker = "普通日志初始化失败"
			}
			if !strings.Contains(string(data), marker) || !strings.Contains(string(data), "snapover.log") {
				t.Fatalf("missing logger initialization diagnostic: %s", data)
			}
			if blocked {
				return
			}
			data, err = os.ReadFile(filepath.Join(dir, "logs", "snapover.log"))
			if err != nil {
				t.Fatal(err)
			}
			for _, message := range []string{"startup without console", "keepalive without console"} {
				if !strings.Contains(string(data), message) {
					t.Fatalf("missing log %q without a console: %s", message, data)
				}
			}
		})
	}
}
