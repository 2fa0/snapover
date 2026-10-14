//go:build !windows && !darwin

package main

import (
	"context"
	"errors"

	"github.com/egoist/mygo"
)

func openSystemImageWithOS(context.Context, string) error {
	return errors.New("当前系统没有可用的图片预览程序")
}

func mygoScreenDisplays() []mygo.Display { return nil }
