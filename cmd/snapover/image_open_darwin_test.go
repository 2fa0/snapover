//go:build darwin

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSystemImageOpenStopsAtDeadline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "open"), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := openSystemImageWithOS(ctx, "unused.png"); err == nil {
		t.Fatal("opening did not report the deadline")
	}
	if ctx.Err() != context.DeadlineExceeded || time.Since(start) > 2*time.Second {
		t.Fatalf("opening did not stop promptly: %v", ctx.Err())
	}
}
