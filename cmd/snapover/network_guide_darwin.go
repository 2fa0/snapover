package main

import (
	"context"

	"github.com/egoist/mygo"
	"golang.org/x/sys/unix"
)

func detectNetworkGuideKind() networkGuideKind {
	version, _ := unix.Sysctl("kern.osproductversion")
	return macNetworkGuideKind(version)
}

func openNetworkGuideSettings(ctx context.Context, _ bool) error {
	// Check cancellation on the UI thread too: a queued launch should not open
	// System Settings after its guide has been closed.
	var err error
	mygo.RunOnMain(func() {
		if err = ctx.Err(); err != nil {
			return
		}
		err = mygo.Shell.OpenExternal("x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_LocalNetwork")
		if err != nil {
			err = mygo.Shell.OpenExternal("x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy")
		}
	})
	return err
}
