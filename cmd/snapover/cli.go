package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"unicode/utf8"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/control"
)

func (a *app) handleCLI(ctx context.Context, request control.Request, progress func(int64, int64)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	link, online := a.engine.Link()
	if !online {
		return "", errors.New("没有在线设备，请先在 Snapover 中连接设备后重试")
	}
	if request.Kind == "file" {
		if !filepath.IsAbs(request.Value) {
			return "", errors.New("文件路径必须是绝对路径")
		}
		name, err := a.engine.SendFile(ctx, link.ID, request.Value, progress)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已保存到 %s 的接收文件夹：%s", displayName(link.Name, link.ID), name), nil
	}
	var item clip.Item
	var err error
	switch request.Kind {
	case "text":
		if !utf8.ValidString(request.Value) {
			return "", errors.New("文本必须使用 UTF-8 编码")
		}
		item, err = clip.FromText(request.Value)
	case "image":
		if !filepath.IsAbs(request.Value) {
			return "", errors.New("图片路径必须是绝对路径")
		}
		item, err = clip.FromImageFile(request.Value)
	default:
		return "", errors.New("不支持此发送类型，请使用 file、text 或 image")
	}
	if err != nil {
		return "", err
	}
	item.SourceApp = "Snapover CLI"
	if err := a.engine.SendClipboard(ctx, link.ID, item); err != nil {
		return "", err
	}
	return fmt.Sprintf("已写入 %s 的剪贴板", displayName(link.Name, link.ID)), nil
}
