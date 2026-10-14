package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWindowsNetworkGuideDoesNotLaunchCancelledSettings(t *testing.T) {
	for _, firewall := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := openNetworkGuideSettings(ctx, firewall); !errors.Is(err, context.Canceled) {
			t.Fatalf("closed guide launched settings (firewall=%v): %v", firewall, err)
		}
		ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		if err := openNetworkGuideSettings(ctx, firewall); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expired settings launch continued (firewall=%v): %v", firewall, err)
		}
		cancel()
	}
}
