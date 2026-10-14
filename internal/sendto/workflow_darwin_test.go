//go:build darwin

package sendto

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWorkflowRunsWithAutomatorAndPreservesPaths(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Snapover 中文 ' $() &.sh")
	result := filepath.Join(dir, "arguments")
	// A fake app captures the exact arguments without launching Snapover or
	// adding a workflow to the user's Services directory.
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s\\0' \"$@\" > '"+result+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	workflow := filepath.Join(dir, workflowName)
	if err := createWorkflow(context.Background(), workflow, exe); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(workflow, "Contents", "Info.plist"), filepath.Join(workflow, "Contents", "Resources", "document.wflow")} {
		if out, err := exec.Command("/usr/bin/plutil", "-lint", path).CombinedOutput(); err != nil {
			t.Fatalf("invalid plist: %v %s", err, out)
		}
	}
	paths := []string{filepath.Join(dir, "中文 报告.pdf"), filepath.Join(dir, "' $() & second.txt")}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/automator", "-i", "-", workflow)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("automator: %v %s", err, out)
	}
	waitWorkflowArguments(t, result, append([]string{"--send-file"}, paths...))

	// Shell arguments also preserve newlines; Finder passes an array rather
	// than the newline-separated input accepted by the automator CLI above.
	if err := os.Remove(result); err != nil {
		t.Fatal(err)
	}
	paths = []string{filepath.Join(dir, "line\nbreak.txt"), filepath.Join(dir, "`touch injected` $HOME.txt")}
	args := append([]string{"-c", workflowScript(exe), "snapover-workflow"}, paths...)
	if out, err := exec.CommandContext(ctx, "/bin/sh", args...).CombinedOutput(); err != nil {
		t.Fatalf("shell: %v %s", err, out)
	}
	waitWorkflowArguments(t, result, append([]string{"--send-file"}, paths...))
}

func waitWorkflowArguments(t *testing.T, path string, want []string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var data []byte
	for time.Now().Before(deadline) {
		data, _ = os.ReadFile(path)
		if bytes.Count(data, []byte{0}) == len(want) {
			got := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("arguments = %q, want %q", got, want)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("workflow did not deliver arguments: %q", data)
}

func TestWorkflowUpdatesMovedAppWithoutRewritingUnchangedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), workflowName)
	ctx := context.Background()
	if err := createWorkflow(ctx, path, "/Applications/Snapover.app/Contents/MacOS/Snapover"); err != nil {
		t.Fatal(err)
	}
	document := filepath.Join(path, "Contents", "Resources", "document.wflow")
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(document, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := createWorkflow(ctx, path, "/Applications/Snapover.app/Contents/MacOS/Snapover"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(document)
	if err != nil || info.ModTime().Unix() != oldTime.Unix() {
		t.Fatalf("unchanged workflow was rewritten: %v", err)
	}
	if err := createWorkflow(ctx, path, "/Users/test/Applications/Snapover.app/Contents/MacOS/Snapover"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(document)
	if err != nil || !bytes.Contains(data, []byte("/Users/test/Applications/Snapover.app/Contents/MacOS/Snapover")) {
		t.Fatalf("workflow did not follow moved app: %v", err)
	}
}

func TestWorkflowCanceledAndMissingApp(t *testing.T) {
	path := filepath.Join(t.TempDir(), workflowName)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := createWorkflow(ctx, path, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled install: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled install created a workflow: %v", err)
	}
	exe := filepath.Join(t.TempDir(), "missing")
	out, err := exec.Command("/bin/sh", "-c", workflowScript(exe), "snapover-workflow", "file.txt").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "重新安装快速操作") {
		t.Fatalf("missing app did not explain recovery: %v %s", err, out)
	}
}
