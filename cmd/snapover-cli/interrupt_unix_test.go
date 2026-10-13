//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"testing"
	"time"
)

func TestInterruptExitsWhileStdinIsOpen(t *testing.T) {
	if os.Getenv("SNAPOVER_CLI_INTERRUPT_TEST") == "1" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		fmt.Fprintln(os.Stdout, "ready")
		os.Exit(run(ctx, []string{"send", "text", "--stdin", "--data", "unused"}, os.Stdin, os.Stdout, os.Stderr))
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestInterruptExitsWhileStdinIsOpen$")
	cmd.Env = append(os.Environ(), "SNAPOVER_CLI_INTERRUPT_TEST=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close() // Keep stdin open until after the process has exited.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("unexpected subprocess output: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("subprocess did not start")
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatal("Ctrl+C did not interrupt stdin read")
	}
	if cmd.ProcessState.ExitCode() != 1 || !strings.Contains(stderr.String(), "已取消发送") {
		t.Fatalf("exit=%d stderr=%s", cmd.ProcessState.ExitCode(), stderr.String())
	}
}
