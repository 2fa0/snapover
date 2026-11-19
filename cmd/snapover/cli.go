package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/control"
)

// savedList keeps a large batch on one readable console line.
func savedList(names []string) string {
	if len(names) <= 3 {
		return strings.Join(names, "、")
	}
	return fmt.Sprintf("%s 等 %d 个文件", strings.Join(names[:3], "、"), len(names))
}

func (a *app) handleCLI(ctx context.Context, request control.Request, progress func(int64, int64)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	link, online := a.engine.Link()
	if !online {
		return "", errors.New("没有在线设备，请先在 Snapover 中连接设备后重试")
	}
	if request.Kind == "file" {
		paths := request.Paths
		if len(paths) == 0 && request.Value != "" {
			// Older console clients send exactly one file in Value.
			paths = []string{request.Value}
		}
		if len(paths) == 0 {
			return "", errors.New("请提供要发送的文件路径")
		}
		for _, path := range paths {
			if !filepath.IsAbs(path) {
				return "", errors.New("文件路径必须是绝对路径")
			}
		}
		saved, err := a.engine.SendFiles(ctx, link.ID, paths, func(_ int, done, total int64) {
			if progress != nil {
				progress(done, total)
			}
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已保存到 %s 的接收文件夹：%s", displayName(link.Name, link.ID), savedList(saved)), nil
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
