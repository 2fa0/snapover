package main

import (
	"context"
	"os/exec"

	"github.com/egoist/mygo"
)

func detectNetworkGuideKind() networkGuideKind { return guideWindows }

func openNetworkGuideSettings(ctx context.Context, firewall bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !firewall {
		var err error
		mygo.RunOnMain(func() {
			if err = ctx.Err(); err == nil {
				err = mygo.Shell.OpenExternal("ms-settings:network-status")
			}
		})
		return err
	}
	cmd := exec.CommandContext(ctx, "control.exe", "/name", "Microsoft.WindowsFirewall")
	return cmd.Run()
}
