//go:build darwin

package main

import (
	"context"
	"os/exec"

	"github.com/egoist/mygo"
)

func openSystemImageWithOS(ctx context.Context, path string) error {
	// Preview, not the default handler and not Quick Look: the history flyout
	// closes when another app activates, and a long screenshot is read there.
	return exec.CommandContext(ctx, "open", "-a", "Preview", "--", path).Run()
}

func mygoScreenDisplays() []mygo.Display { return mygo.Screen.Displays() }
