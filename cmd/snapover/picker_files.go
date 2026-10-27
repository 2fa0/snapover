package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/history"
)

// The history row stays compact. Only its hover preview shows the tree;
// long names stay on one line and reveal their full name in a tooltip.
func pickFilePreview(c *ui.Context, preview history.FilePreview, err error) {
	t := c.Theme()
	if err != nil {
		ui.Text(c, "文件预览加载失败，请重新悬停重试").TextColor(t.TextMuted)
		return
	}
	ui.Text(c, preview.Name).Bold().SingleLine().Tooltip(preview.Name).Shrink(0)
	ui.Scroll(c.Key("file-tree")).Grow(1).FillWidth().Gap(4).Children(func() {
		if preview.Directory && len(preview.Items) == 0 {
			ui.Text(c, "└─ 空文件夹").FontSize(12).TextColor(t.TextMuted)
		}
		for i, item := range preview.Items {
			ui.Row(c.Key(i)).Gap(6).Padding(0, float32(item.Depth*16)).AlignItems(ui.Center).Children(func() {
				branch := "├─"
				if i == len(preview.Items)-1 || preview.Items[i+1].Depth < item.Depth {
					branch = "└─"
				}
				ui.Text(c, branch).FontSize(12).TextColor(t.TextMuted)
				icon := textIcon
				if item.Directory {
					icon = folderIcon
				}
				ui.Icon(c, icon).FontSize(14).TextColor(t.TextMuted)
				ui.Text(c, item.Name).FontSize(12).Grow(1).SingleLine().Tooltip(item.Name)
			})
		}
	})
	if preview.More {
		ui.Text(c, "还有更多项目 · 仅展示一层，最多 20 项").FontSize(11).TextColor(t.TextMuted).Shrink(0)
	} else if preview.Directory || len(preview.Items) > 0 {
		ui.Text(c, "仅展示一层，最多 20 项").FontSize(11).TextColor(t.TextMuted).Shrink(0)
	}
}
