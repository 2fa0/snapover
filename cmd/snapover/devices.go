package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/discovery"
	"github.com/2fa0/snapover/internal/identity"
)

func (a *app) devicesPage(c *ui.Context) {
	t := c.Theme()
	pageHeading(c, "连接设备", "连接同一局域网内的电脑，配对后自动重连。")
	a.networkSetupHint(c)
	pending, waiting := a.engine.PendingPair()
	until := a.engine.PairingUntil()
	adding := time.Now().Before(until)
	if adding || waiting {
		c.After(time.Second)
	}
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "添加一台电脑").Bold()
			message := "在两台电脑上打开 Snapover，并点击「添加设备」。"
			if adding && !waiting {
				message = fmt.Sprintf("正在接受配对请求 · 剩余 %d 秒", max(0, int(time.Until(until).Seconds())+1))
			}
			ui.Text(c, message).FontSize(12).TextColor(t.TextMuted)
		})
		if !adding && !waiting {
			if ui.PrimaryButton(c, "添加设备").Clicked() {
				a.engine.SetPairing(true)
				a.status = "请选择发现的设备，并在另一台电脑上点击「添加设备」"
			}
		} else if ui.Button(c, "取消添加").Clicked() {
			a.engine.SetPairing(false)
			if !a.isPaired(a.targetPeerID) {
				a.stopDial()
			}
			a.status = "已取消添加设备"
		}
	})
	if waiting {
		ui.Column(c.Key("pair-confirmation")).Padding(24).Gap(14).Radius(12).
			Background(t.Surface).Border(1, t.Accent).Transition(ui.ElementTransition{
			Duration: 180 * time.Millisecond, Enter: &ui.Motion{Y: 6},
		}).Children(func() {
			ui.Text(c, "核对校验码").FontSize(18).Bold()
			ui.Textf(c, "%s · %s", displayName(pending.Name, pending.PeerID), platformLabel(pending.Platform)).TextColor(t.TextMuted)
			ui.Text(c, pending.Code).FontSize(40).Bold().FontFeatures("tnum").Selectable().Padding(6, 0)
			ui.Text(c, "请确认另一台电脑显示相同的六位数字。双方确认后，才会开始同步剪贴板和接收文件。").TextColor(t.TextMuted)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				label := "校验码相同，确认配对"
				if pending.Confirmed {
					label = "等待对方确认"
				}
				if ui.PrimaryButton(c, label).Disabled(pending.Confirmed).Clicked() {
					a.engine.ConfirmPair(pending.Token, true)
				}
				if ui.Button(c, "拒绝配对").Disabled(pending.Confirmed).Clicked() {
					a.engine.ConfirmPair(pending.Token, false)
				}
			})
			ui.Textf(c, "剩余 %d 秒", max(0, int(time.Until(pending.Expires).Seconds())+1)).FontSize(12).TextColor(t.TextMuted)
		})
	}

	settingsGroup(c, "已配对设备", func() {
		link, up := a.engine.Link()
		if len(a.devices) == 0 {
			emptyState(c, deviceIcon, "还没有配对设备", "点击「添加设备」，选择同一网络内的电脑。")
		}
		for _, dev := range a.devices {
			online := up && link.ID == dev.ID
			ui.Column(c.Key("paired-" + dev.ID)).Gap(12).Children(func() {
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, deviceIcon).FontSize(26).TextColor(t.TextMuted)
					ui.Column(c).Grow(1).Gap(4).Children(func() {
						ui.Text(c, displayName(dev.Peer.Name, dev.ID)).Bold().SingleLine()
						ui.Text(c, platformLabel(dev.Peer.Platform)).FontSize(12).TextColor(t.TextMuted)
					})
					state, color := "离线", t.TextMuted
					if online {
						state, color = "已连接", t.Success
					} else if a.targetPeerID == dev.ID && a.dialCancel != nil {
						state = "正在重连"
					}
					ui.Text(c, state).FontSize(12).TextColor(color).SingleLine().ContentTransition(ui.ContentTransition{})
					if !online && ui.Button(c, "连接").Disabled(waiting || dev.Peer.Addr == "").Clicked() {
						addr := dev.Peer.Addr
						for _, found := range a.discovered {
							if found.ID == dev.ID {
								addr = found.Addr
								break
							}
						}
						a.connectDevice(dev.ID, addr)
					}
					if ui.Button(c, "详情").Clicked() {
						if a.deviceDetails == dev.ID {
							a.deviceDetails = ""
						} else {
							a.deviceDetails = dev.ID
						}
					}
				})
				if a.deviceDetails == dev.ID {
					ui.Column(c.Key("details")).Gap(10).Padding(12).Radius(8).Background(t.Background).
						Transition(ui.ElementTransition{
							Duration: 160 * time.Millisecond,
							Enter:    &ui.Motion{Collapse: true},
							Exit:     &ui.Motion{Collapse: true},
						}).Children(func() {
						kv(c, "地址", dev.Peer.Addr)
						ui.Text(c, "证书指纹").FontSize(12).TextColor(t.TextMuted)
						ui.Text(c, identity.FormatFingerprint(dev.Peer.Fingerprint)).Selectable().FontSize(12)
						if a.confirmRemove == dev.ID {
							ui.Text(c, "解除后立即断开连接，再次添加需要双方确认。").FontSize(12).TextColor(t.TextMuted)
							ui.Row(c).Gap(8).Children(func() {
								if ui.Button(c, "确认解除配对").TextColor(t.Danger).Clicked() {
									a.forget(dev.ID)
								}
								if ui.Button(c, "取消").Clicked() {
									a.confirmRemove = ""
								}
							})
						} else if ui.Button(c, "解除配对").TextColor(t.Danger).Clicked() {
							a.confirmRemove = dev.ID
						}
					})
				}
			})
		}
	})

	settingsGroup(c, "附近的设备", func() {
		count := 0
		for _, dev := range a.discovered {
			if a.isPaired(dev.ID) {
				continue
			}
			count++
			ui.Row(c.Key("found-"+dev.ID)).Gap(12).Padding(4, 0).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, deviceIcon).FontSize(24).TextColor(t.TextMuted)
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, displayName(dev.Name, dev.ID)).Bold().SingleLine()
					ui.Text(c, platformLabel(dev.Platform)).FontSize(12).TextColor(t.TextMuted)
				})
				connecting := a.dialCancel != nil && a.targetPeerID == dev.ID
				label := "配对"
				if connecting {
					label = "正在连接"
				}
				if ui.Button(c, label).Disabled(waiting || connecting).Clicked() {
					a.connectDevice(dev.ID, dev.Addr)
				}
			})
		}
		if count == 0 && a.discoveryMessage == "" {
			ui.Text(c, "暂未发现其他电脑").Bold()
			ui.Text(c, "确认对端已打开 Snapover，并连接同一 Wi-Fi 或有线网络。也可以在下方输入地址。").FontSize(12).TextColor(t.TextMuted)
		}
		if a.discoveryMessage != "" {
			ui.Text(c, a.discoveryMessage).FontSize(12).TextColor(t.TextMuted)
		}
		a.networkHelp(c)
	})
	ui.Column(c).Gap(10).Children(func() {
		ui.Text(c, "通过地址连接").Bold()
		ui.Row(c).Gap(8).Children(func() {
			submit := ui.TextInput(c.Key("peer"), &a.peer).Label("对端局域网地址").Placeholder("192.168.1.20，端口可省略").Grow(1).Submitted()
			if ui.Button(c, "连接地址").Disabled(waiting).Clicked() || (submit && !waiting) {
				a.targetPeerID = ""
				a.connect()
			}
		})
		ui.Text(c, "首次连接仍需双方点击「添加设备」并核对校验码。").FontSize(12).TextColor(t.TextMuted)
	})
	ui.Collapsible(c, "广播网段与配对管理", &a.showNetwork, func() {
		ui.Column(c).Gap(14).Padding(12, 0).Children(func() {
			a.broadcastSection(c)
			if len(a.devices) > 0 {
				ui.Divider(c)
				label := "解除全部配对"
				if a.confirm {
					label = "确认解除全部配对"
					ui.Text(c, "立即断开连接，再次添加任何设备都需要双方确认。").FontSize(12).TextColor(t.TextMuted)
				}
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, label).TextColor(t.Danger).Clicked() {
						if !a.confirm {
							a.confirm = true
						} else {
							a.clearPairs()
						}
					}
					if a.confirm && ui.Button(c, "取消").Clicked() {
						a.confirm = false
					}
				})
			}
		})
	})
}

func (a *app) broadcastSection(c *ui.Context) {
	t := c.Theme()
	section(c, "广播网段")
	ui.Text(c, "只在勾选的网段里广播和搜索。全选时，之后新出现的物理网段也会加入。").TextColor(t.TextMuted)
	rows := liveSegmentRows()
	sel := a.selection()
	var physical, virtual []segmentRow
	var keys []string
	for _, row := range rows {
		if row.virtual {
			virtual = append(virtual, row)
			continue
		}
		physical = append(physical, row)
		keys = append(keys, row.key)
	}
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, "全选").Disabled(sel.All()).Clicked() {
			a.setDiscovery(discovery.SelectAll())
		}
		if ui.Button(c, "全不选").Disabled(sel.Stopped()).Clicked() {
			a.setDiscovery(discovery.SelectNone(keys))
		}
	})
	sel = a.selection()
	if len(physical) == 0 {
		ui.Text(c, "没有可用的局域网网段。连接 Wi-Fi 或网线后会出现在这里。").TextColor(t.TextMuted)
	}
	a.syncSegmentChecks(physical, sel)
	for i := range a.segments {
		row := &a.segments[i]
		if ui.Checkbox(c.Key("seg-"+row.key), &row.checked, row.label).Changed() {
			a.setDiscovery(discovery.Toggle(sel, keys, row.key, row.checked))
			sel = a.selection()
		}
	}
	if !sel.All() && !sel.Stopped() {
		ui.Text(c, "只广播已勾选的网段。新出现的默认不加入。").FontSize(12).TextColor(t.TextMuted)
	}
	if sel.HasNew(keys) {
		ui.Text(c, "有新网段未加入广播。").FontSize(12).TextColor(t.TextMuted)
	} else if sel.Stopped() && len(physical) > 0 {
		ui.Text(c, "发现已停止。勾选网段，或在下面手填地址。").FontSize(12).TextColor(t.TextMuted)
	}
	for _, row := range virtual {
		ui.Text(c, row.label).FontSize(12).TextColor(t.TextMuted)
	}
}

func (a *app) isPaired(id string) bool {
	for _, dev := range a.devices {
		if dev.ID == id {
			return true
		}
	}
	return false
}

func (a *app) connectDevice(id, addr string) {
	a.targetPeerID = id
	a.peer = addr
	a.connect()
}

func (a *app) applyDiscovery(snapshot discovery.Snapshot) {
	if snapshot.Scope != nil && !snapshot.Scope.Equal(a.currentDiscoveryScope()) {
		return
	}
	a.discovered = snapshot.Devices
	a.discoveryMessage = snapshot.Message
	if a.engine.Connected() || a.targetPeerID == "" || !a.isPaired(a.targetPeerID) {
		return
	}
	for _, dev := range snapshot.Devices {
		if dev.ID == a.targetPeerID && dev.Addr != a.dialAddr {
			a.peer = dev.Addr
			a.connect()
			a.status = fmt.Sprintf("已找到 %s 的新地址，正在重连", displayName(dev.Name, dev.ID))
			return
		}
	}
}

func (a *app) clearPairs() {
	a.confirm = false
	if err := a.engine.Revoke(""); err != nil {
		a.status = "解除全部配对失败，请重试"
		return
	}
	a.stopDial()
	a.devices = nil
	a.peer = ""
	a.targetPeerID = ""
	a.status = "已解除全部配对并断开连接。再次添加需要双方确认"
}
