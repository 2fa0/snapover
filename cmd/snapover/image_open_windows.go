//go:build windows

package main

import (
	"context"

	"github.com/egoist/mygo"
)

func openSystemImageWithOS(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return mygo.Shell.OpenPath(path)
}
