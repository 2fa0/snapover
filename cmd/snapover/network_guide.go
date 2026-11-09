package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type networkGuideKind uint8

const (
	guideUnsupported networkGuideKind = iota
	guideWindows
	guideMacLegacy
	guideMacLocalNetwork
)

// Only this small platform value is retained; guide content is built on demand.
var currentNetworkGuideKind = detectNetworkGuideKind()

const networkGuideSearchDelay = 10 * time.Second

func macNetworkGuideKind(version string) networkGuideKind {
	major, _, _ := strings.Cut(version, ".")
	if n, err := strconv.Atoi(major); err == nil && n >= 15 {
		return guideMacLocalNetwork
	}
	return guideMacLegacy
}

func networkGuideDismissed(values *settings.Values, kind networkGuideKind) *bool {
	if kind == guideWindows {
		return &values.WindowsNetworkGuideDismissed
	}
	return &values.MacNetworkGuideDismissed
}

// Only the open guide owns this state. Closing it also detaches its UI and
// cancels any outstanding settings launch; the background keeps no guide cache.
type networkGuide struct {
	app             *app
	kind            networkGuideKind
	win             *mygo.Window
	ctx             context.Context
	cancel          context.CancelFunc
	searchStarted   time.Time
	showSettings    bool
	autoExpanded    bool
	showDiagnostics bool
	openingSettings bool
	message         string
}

func (a *app) networkSetupHint(c *ui.Context) {
	kind := currentNetworkGuideKind
	if (kind != guideWindows && kind != guideMacLocalNetwork) || a.cfg == nil || *networkGuideDismissed(&a.cfg.Values, kind) || len(a.devices) != 0 {
		return
	}
	t := c.Theme()
	ui.Column(c).Gap(8).Children(func() {
		access := a.tr("打开本地网络设置")
		if kind == guideWindows {
			access = a.tr("打开防火墙设置")
			alertMessage(c, a.tr("允许 Snapover 连接另一台电脑"), a.tr("出现防火墙提示时，允许 Snapover 在可信的专用网络上通信。"), infoIcon, t.Accent)
		} else {
			alertMessage(c, a.tr("允许 Snapover 查找另一台电脑"), a.tr("系统询问本地网络权限时，选择「允许」。"), infoIcon, t.Accent)
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c, access).Disabled(a.openingNetworkSettings).Clicked() {
				a.openNetworkAccessSettings()
			}
			if ui.Button(c, a.tr("连接帮助")).Clicked() {
				a.openNetworkGuide()
			}
			if ui.Button(c, a.tr("不再提示")).Clicked() {
				a.dismissNetworkGuide(kind)
			}
		})
	})
}

func (a *app) openNetworkAccessSettings() {
	if a.openingNetworkSettings {
		return
	}
	a.openingNetworkSettings = true
	parent := a.root
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	go func() {
		defer cancel()
		err := openNetworkGuideSettings(ctx, true)
		a.update(func() {
			a.openingNetworkSettings = false
			if err != nil {
				a.status = "打开系统设置失败，请按连接帮助中的步骤手动打开"
			}
		})
	}()
}

func (a *app) networkHelp(c *ui.Context) {
	if currentNetworkGuideKind != guideUnsupported && ui.Button(c, a.tr("连接帮助")).Clicked() {
		a.openNetworkGuide()
	}
}

func (a *app) dismissNetworkGuide(kind networkGuideKind) {
	if a.cfg == nil {
		return
	}
	dismissed := networkGuideDismissed(&a.cfg.Values, kind)
	if *dismissed {
		return
	}
	*dismissed = true
	if err := a.cfg.Save(); err != nil {
		*dismissed = false
		a.status = "保存指引设置失败，请检查数据目录权限后重新关闭指引重试"
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

func (a *app) openNetworkGuide() {
	if a.quitting {
		return
	}
	if g := a.networkGuide; g != nil {
		g.win.Show()
		g.win.Focus()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	kind := currentNetworkGuideKind
	g := &networkGuide{app: a, kind: kind, ctx: ctx, cancel: cancel, searchStarted: time.Now()}
	win := mygo.NewWindow(a.placeOnWorkingDisplay(mygo.WindowOptions{
		Title: a.tr("连接指引 · Snapover"), Width: 600, Height: 620,
		MinWidth: 500, MinHeight: 480, Content: ui.View(g.view),
	}))
	g.win, a.networkGuide = win, g
	offFocus := win.OnFocus(g.retry)
	win.OnClosed(func() {
		offFocus()
		cancel()
		a.networkGuide = nil
		*g = networkGuide{}
		if !a.quitting {
			a.dismissNetworkGuide(kind)
		}
	})
	g.retry()
}

func (a *app) refreshNetworkGuide() {
	if g := a.networkGuide; g != nil {
		if a.engine.Connected() {
			g.win.Close()
		} else {
			g.win.Invalidate()
		}
	}
}

func (g *networkGuide) retry() {
	if g.app == nil {
		return
	}
	g.searchStarted = time.Now()
	g.app.retryDiscovery()
	if g.win != nil {
		g.win.Invalidate()
	}
}

func (a *app) retryDiscovery() {
	if a.discoveryWake != nil {
		select {
		case a.discoveryWake <- struct{}{}:
		default:
		}
	}
}

func (g *networkGuide) view(c *ui.Context) {
	if g.app == nil {
		return
	}
	a, t := g.app, c.Theme()
	ui.Scroll(c).Fill().Padding(24).Gap(16).Background(t.Background).Children(func() {
		ui.Text(c, a.tr("连接另一台电脑")).FontSize(22).Bold()
		if count := len(a.discovered); count > 0 {
			ui.Text(c, a.tr(fmt.Sprintf("已发现 %d 台电脑", count))).Bold()
		} else if remaining := networkGuideSearchDelay - time.Since(g.searchStarted); remaining > 0 && a.discoveryMessage == "" {
			ui.Text(c, a.tr("正在搜索同一网络内的电脑…")).Bold()
			c.After(remaining)
		} else {
			ui.Text(c, a.tr("暂未发现其他电脑")).Bold()
			if !g.autoExpanded {
				g.showSettings, g.autoExpanded = true, true
			}
		}
		if a.discoveryMessage != "" {
			ui.Text(c, a.tr(a.discoveryMessage)).FontSize(12).TextColor(t.TextMuted)
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, a.tr("重新搜索")).Clicked() {
				g.retry()
			}
			if ui.Button(c, a.tr("返回连接设备")).Clicked() {
				a.reveal(pageDevices)
			}
		})
		if g.kind == guideWindows {
			g.windowsSettings(c)
		} else if g.kind == guideMacLocalNetwork {
			g.macSettings(c)
		}
		ui.Collapsible(c, a.tr("仍然无法连接？"), &g.showDiagnostics, func() {
			ui.Column(c).Gap(8).Padding(0, 10).Children(func() {
				ui.Text(c, a.tr("请在两台电脑上分别检查。访客 Wi-Fi 或网络隔离可能阻止电脑互相连接。"))
				if g.kind == guideMacLocalNetwork {
					ui.Text(c, a.tr("可以在「连接设备」中手填对端地址，绕过自动发现；仍需允许本地网络访问和防火墙连接。"))
				} else {
					ui.Text(c, a.tr("可以在「连接设备」中手填对端地址，绕过自动发现；仍需防火墙允许连接。"))
				}
				ui.Text(c, a.tr(fmt.Sprintf("本机连接端口为 TCP %s，自动发现使用 UDP 5353。", portOf(a.listen)))).FontSize(12).TextColor(t.TextMuted)
			})
		})
		if g.message != "" {
			ui.Text(c, a.tr(g.message)).TextColor(t.TextMuted)
		}
		if ui.PrimaryButton(c, a.tr("关闭指引")).Clicked() && g.win != nil {
			g.win.Close()
		}
	})
}

func (g *networkGuide) windowsSettings(c *ui.Context) {
	a, t := g.app, c.Theme()
	ui.Collapsible(c, a.tr("查看防火墙设置方法"), &g.showSettings, func() {
		ui.Column(c).Gap(10).Padding(0, 10).Children(func() {
			ui.Text(c, a.tr("1. 打开防火墙设置，选择「允许应用或功能通过 Windows Defender 防火墙」。"))
			ui.Text(c, a.tr("2. 点击「更改设置」，找到 Snapover，勾选应用及「专用」网络。"))
			ui.Text(c, a.tr("3. 如果列表中没有 Snapover，点击「允许其他应用」，选择正在使用的 Snapover.exe。"))
			if path, err := os.Executable(); err == nil {
				ui.Text(c, a.tr("当前程序："+path)).FontSize(12).TextColor(t.TextMuted).Selectable()
			}
			ui.Text(c, a.tr("更改设置可能需要管理员权限。设置后返回此窗口，会立即重新搜索。")).FontSize(12).TextColor(t.TextMuted)
			if ui.Button(c, a.tr("打开防火墙设置")).Disabled(g.openingSettings).Clicked() {
				g.openSettings(true)
			}
		})
	})
	ui.Text(c, a.tr("如果当前网络是「公用」，请先确认它是否可信。信任的家庭或办公网络可以改为「专用」，让放行规则与当前网络类型匹配。")).FontSize(12).TextColor(t.TextMuted)
	if ui.Button(c, a.tr("打开网络设置")).Disabled(g.openingSettings).Clicked() {
		g.openSettings(false)
	}
}

func (g *networkGuide) macSettings(c *ui.Context) {
	a, t := g.app, c.Theme()
	ui.Collapsible(c, a.tr("查看本地网络权限设置方法"), &g.showSettings, func() {
		ui.Column(c).Gap(10).Padding(0, 10).Children(func() {
			ui.Text(c, a.tr("1. 打开「系统设置 → 隐私与安全性 → 本地网络」。"))
			ui.Text(c, a.tr("2. 找到 Snapover，开启它的本地网络访问。"))
			ui.Text(c, a.tr("3. 返回这个指引窗口，会立即重新搜索；也可以点击「重新搜索」。"))
			ui.Text(c, a.tr("如果列表中没有 Snapover，请先点击「重新搜索」，并留意系统的授权弹窗。")).FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, a.tr("若按钮只打开了「隐私与安全性」，请手动选择「本地网络」。")).FontSize(12).TextColor(t.TextMuted)
			if ui.Button(c, a.tr("打开本地网络设置")).Disabled(g.openingSettings).Clicked() {
				g.openSettings(true)
			}
		})
	})
}

func (g *networkGuide) openSettings(access bool) {
	if g.openingSettings {
		return
	}
	g.openingSettings, g.message = true, ""
	a, win := g.app, g.win
	ctx, cancel := context.WithTimeout(g.ctx, 5*time.Second)
	// Capture the owner and window identity, not the guide's temporary state.
	go func() {
		defer cancel()
		err := openNetworkGuideSettings(ctx, access)
		a.update(func() {
			current := a.networkGuide
			if current == nil || current.win != win {
				return
			}
			current.openingSettings = false
			if err != nil {
				current.message = "打开系统设置失败，请按上面的步骤手动打开设置后重试"
			}
		})
	}()
}
