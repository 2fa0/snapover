package main

import (
	"time"

	"github.com/2fa0/snapover/internal/discovery"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/lan"
	"github.com/egoist/mygo/ui"
)

func (a *app) devicesPage(c *ui.Context) {
	t := c.Theme()
	pending, waiting := a.engine.PendingPair()
	until := a.engine.PairingUntil()
	adding := time.Now().Before(until)
	if adding || waiting {
		c.After(time.Second)
	}
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			if adding && !waiting {
				ui.Text(c, a.trf("等待配对 · %d 秒", "Waiting to pair · %ds", max(0, int(time.Until(until).Seconds())+1))).FontSize(12).TextColor(t.TextMuted)
			}
		})
		if !adding && !waiting {
			if press(ui.PrimaryButton(c, a.tr("添加设备"))).Clicked() {
				a.engine.SetPairing(true)
				a.status = "正在添加设备"
			}
		} else if ui.Button(c, a.tr("取消添加")).Clicked() {
			a.engine.SetPairing(false)
			if !a.isPaired(a.targetPeerID) {
				a.stopDial()
			}
			a.status = "已取消添加设备"
		}
	})
	if adding && !waiting {
		a.networkSetupHint(c)
	}
	if waiting {
		ui.Column(c.Key("pair-confirmation")).Padding(24).Gap(14).Radius(12).
			Background(t.Surface).Border(1, t.Accent).Transition(ui.ElementTransition{
			Duration: 180 * time.Millisecond, Enter: &ui.Motion{Y: 6},
		}).Children(func() {
			ui.Text(c, a.tr("核对校验码")).FontSize(18).Bold()
			ui.Textf(c, "%s · %s", a.displayName(pending.Name, pending.PeerID), a.tr(platformLabel(pending.Platform))).TextColor(t.TextMuted)
			ui.Text(c, pending.Code).FontSize(40).Bold().FontFeatures("tnum").Selectable().Padding(6, 0)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				label := a.tr("确认配对")
				if pending.Confirmed {
					label = a.tr("等待对方确认")
				}
				if press(ui.PrimaryButton(c, label).Disabled(pending.Confirmed)).Clicked() {
					a.engine.ConfirmPair(pending.Token, true)
				}
				if press(ui.Button(c, a.tr("拒绝配对")).Disabled(pending.Confirmed)).Clicked() {
					a.engine.ConfirmPair(pending.Token, false)
				}
			})
			ui.Text(c, a.trf("剩余 %d 秒", "%ds remaining", max(0, int(time.Until(pending.Expires).Seconds())+1))).FontSize(12).TextColor(t.TextMuted)
		})
	}

	if len(a.devices) > 0 {
		settingsGroup(c, a.tr("已配对设备"), func() {
			link, up := a.engine.Link()
			for _, dev := range a.devices {
				online := up && link.ID == dev.ID
				ui.Column(c.Key("paired-" + dev.ID)).Gap(12).Children(func() {
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, deviceIcon).FontSize(26).TextColor(t.TextMuted)
						ui.Column(c).Grow(1).Gap(4).Children(func() {
							ui.Text(c, a.displayName(dev.Peer.Name, dev.ID)).Bold().SingleLine()
							ui.Text(c, a.tr(platformLabel(dev.Peer.Platform))).FontSize(12).TextColor(t.TextMuted)
						})
						state, color := "离线", t.TextMuted
						if online {
							state, color = "已连接 · "+link.Route.Label(), t.Success
						} else if a.targetPeerID == dev.ID && a.dialCancel != nil {
							state = "正在重连"
						}
						ui.Text(c, a.tr(state)).FontSize(12).TextColor(color).SingleLine().ContentTransition(ui.ContentTransition{})
						endpoints := dev.Peer.Endpoints(a.allowTailscale)
						if !online && ui.Button(c, a.tr("连接")).Disabled(waiting || len(endpoints) == 0).Clicked() {
							a.connectDevice(dev.ID, endpoints[0].Addr)
						}
						if ui.Button(c, a.tr("详情")).Clicked() {
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
							a.tailscaleDeviceAddress(c, dev, waiting)
							ui.Text(c, a.tr("证书指纹")).FontSize(12).TextColor(t.TextMuted)
							ui.Text(c, identity.FormatFingerprint(dev.Peer.Fingerprint)).Selectable().FontSize(12)
							if a.confirmRemove == dev.ID {
								ui.Text(c, a.tr("再次添加需要双方确认。")).FontSize(12).TextColor(t.TextMuted)
								ui.Row(c).Gap(8).Children(func() {
									if ui.Button(c, a.tr("确认解除配对")).TextColor(t.Danger).Clicked() {
										a.forget(dev.ID)
									}
									if ui.Button(c, a.tr("取消")).Clicked() {
										a.confirmRemove = ""
									}
								})
							} else if ui.Button(c, a.tr("解除配对")).TextColor(t.Danger).Clicked() {
								a.confirmRemove = dev.ID
							}
						})
					}
				})
			}
		})
	}

	if !waiting && (len(a.devices) == 0 || adding || a.discoveryMessage != "") {
		settingsGroup(c, a.tr("附近的设备"), func() {
			count := 0
			for _, dev := range a.discovered {
				if a.isPaired(dev.ID) {
					continue
				}
				count++
				ui.Row(c.Key("found-"+dev.ID)).Gap(12).Padding(4, 0).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, deviceIcon).FontSize(24).TextColor(t.TextMuted)
					ui.Column(c).Grow(1).Gap(4).Children(func() {
						ui.Text(c, a.displayName(dev.Name, dev.ID)).Bold().SingleLine()
						ui.Text(c, a.tr(platformLabel(dev.Platform))).FontSize(12).TextColor(t.TextMuted)
					})
					connecting := a.dialCancel != nil && a.targetPeerID == dev.ID
					label := a.tr("配对")
					if connecting {
						label = a.tr("正在连接")
					}
					if ui.Button(c, label).Disabled(waiting || connecting).Clicked() {
						a.connectDevice(dev.ID, dev.Addr)
					}
				})
			}
			if count == 0 && a.discoveryMessage == "" {
				ui.Text(c, a.tr("暂未发现其他电脑")).Bold()
			}
			if a.discoveryMessage != "" {
				ui.Text(c, a.tr(a.discoveryMessage)).FontSize(12).TextColor(t.TextMuted)
			}
		})
	}
	ui.Collapsible(c, a.tr("通过地址连接"), &a.showAddress, func() {
		ui.Column(c).Gap(10).Padding(12, 0).Children(func() {
			ui.Segmented(c, &a.addressRoute, a.tr("局域网"), "Tailscale").Label(a.tr("连接方式"))
			placeholder := a.tr("192.168.1.20，端口可省略")
			if a.addressRoute == 1 {
				placeholder = a.tr("100.x.y.z，端口可省略")
				ui.Text(c, a.tr("两端需开启 Tailscale 连接，首次配对仍需双方核对校验码。")).FontSize(12).TextColor(t.TextMuted)
			}
			ui.Row(c).Gap(8).Children(func() {
				submit := ui.TextInput(c.Key("peer"), &a.peer).Label(a.tr("对端地址")).Placeholder(placeholder).Grow(1).Submitted()
				if ui.Button(c, a.tr("连接地址")).Disabled(waiting).Clicked() || (submit && !waiting) {
					a.connectAddress()
				}
			})
		})
	})
	ui.Collapsible(c, a.tr("广播网段与配对管理"), &a.showNetwork, func() {
		ui.Column(c).Gap(14).Padding(12, 0).Children(func() {
			a.broadcastSection(c)
			if len(a.devices) > 0 {
				ui.Divider(c)
				label := a.tr("解除全部配对")
				if a.confirm {
					label = a.tr("确认解除全部配对")
					ui.Text(c, a.tr("再次添加任何设备都需要双方确认。")).FontSize(12).TextColor(t.TextMuted)
				}
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, label).TextColor(t.Danger).Clicked() {
						if !a.confirm {
							a.confirm = true
						} else {
							a.clearPairs()
						}
					}
					if a.confirm && ui.Button(c, a.tr("取消")).Clicked() {
						a.confirm = false
					}
				})
			}
		})
	})
	a.networkHelp(c)
}

func (a *app) broadcastSection(c *ui.Context) {
	t := c.Theme()
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
		if ui.Button(c, a.tr("全选")).Disabled(sel.All()).Clicked() {
			a.setDiscovery(discovery.SelectAll())
		}
		if ui.Button(c, a.tr("全不选")).Disabled(sel.Stopped()).Clicked() {
			a.setDiscovery(discovery.SelectNone(keys))
		}
	})
	sel = a.selection()
	if len(physical) == 0 {
		ui.Text(c, a.tr("没有可用的局域网网段。连接 Wi-Fi 或网线后会出现在这里。")).TextColor(t.TextMuted)
	}
	a.syncSegmentChecks(physical, sel)
	for i := range a.segments {
		row := &a.segments[i]
		if ui.Checkbox(c.Key("seg-"+row.key), &row.checked, a.tr(row.label)).Changed() {
			a.setDiscovery(discovery.Toggle(sel, keys, row.key, row.checked))
			sel = a.selection()
		}
	}
	if sel.HasNew(keys) {
		ui.Text(c, a.tr("有新网段未加入广播。")).FontSize(12).TextColor(t.TextMuted)
	} else if sel.Stopped() && len(physical) > 0 {
		ui.Text(c, a.tr("发现已停止。勾选网段或通过地址连接。")).FontSize(12).TextColor(t.TextMuted)
	}
	for _, row := range virtual {
		ui.Text(c, a.tr(row.label)).FontSize(12).TextColor(t.TextMuted)
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
	if a.targetPeerID == "" || !a.isPaired(a.targetPeerID) {
		return
	}
	if a.dialTarget != nil {
		a.refreshConnectionCandidates()
		return
	}
	if !a.engine.Connected() {
		for _, dev := range snapshot.Devices {
			if dev.ID == a.targetPeerID {
				a.startConnection(lan.Endpoint{Addr: dev.Addr, Route: lan.RouteLAN}, dev.ID, false)
				return
			}
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
