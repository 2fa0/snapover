package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestMacNetworkGuideSettingsLaunchAndCancellation(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := openNetworkGuideSettings(ctx, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("a closed guide should not launch settings: %v", err)
	}
	if currentNetworkGuideKind != guideMacLocalNetwork {
		t.Skip("local network privacy requires macOS 15 or later")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := openNetworkGuideSettings(ctx, true); err != nil {
		t.Fatalf("open local network settings: %v", err)
	}
}
