package main

import (
	"testing"

	"github.com/egoist/mygo"
)

// macOS 的窗口菜单栏只保留应用菜单和编辑菜单，页面切换留给侧栏。
func TestWindowAppMenuKeepsOnlyAppAndEditMenus(t *testing.T) {
	a := interfaceTestApp(t)
	items := a.windowAppMenu().Items()
	if len(items) != 2 || items[0].Role != mygo.RoleAppMenu || items[1].Role != mygo.RoleEditMenu {
		t.Fatalf("top level = %d items, want app menu and edit menu only", len(items))
	}
	var settings *mygo.MenuItem
	var labels []string
	for _, item := range items[0].Submenu {
		if item.Type == mygo.MenuItemSeparator || item.Label == "" {
			continue
		}
		labels = append(labels, item.Label)
		if item.Label == "设置…" {
			settings = item
		}
	}
	for _, label := range labels {
		switch label {
		case "连接设备", "复制历史", "打开窗口":
			t.Fatalf("app menu still duplicates sidebar navigation: %v", labels)
		}
	}
	if settings == nil || settings.Accelerator != "CmdOrCtrl+," || settings.Click == nil {
		t.Fatalf("settings item = %+v, want 设置… with Cmd+,", settings)
	}
}
