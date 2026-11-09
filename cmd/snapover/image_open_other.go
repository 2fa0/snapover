//go:build !windows && !darwin

package main

import (
	"context"
	"errors"
)

func openSystemImageWithOS(context.Context, string) error {
	return errors.New("当前系统没有可用的图片预览程序")
}
