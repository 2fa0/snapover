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
	err := context.Canceled
	mygo.RunOnMain(func() {
		err = ctx.Err()
		if err == nil {
			err = mygo.Shell.OpenPath(path)
		}
	})
	return err
}
