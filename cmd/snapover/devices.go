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
	ui.Text(c, "连接设备").FontSize(22).Bold()
	ui.Text(c, "首次配对需要在两台电脑上添加设备，核对校验码并确认。配对后会自动重连。").TextColor(t.TextMuted)
	pending, waiting := a.engine.PendingPair()
	until := a.engine.PairingUntil()
	adding := time.Now().Before(until)
	if adding || waiting {
		c.After(time.Second)
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if !adding && !waiting {
			if ui.PrimaryButton(c, "添加设备").Clicked() {
				a.engine.SetPairing(true)
				a.status = "请选择发现的设备，并在另一台电脑上点击「添加设备」"
			}
		} else {
			if ui.Button(c, "取消添加").Clicked() {
				a.engine.SetPairing(false)
				if !a.isPaired(a.targetPeerID) {
					a.stopDial()
				}
				a.status = "已取消添加设备"
			}
			if adding && !waiting {
				ui.Textf(c, "允许新设备请求配对 · 剩余 %d 秒", int(time.Until(until).Seconds())+1).TextColor(t.TextMuted)
			}
		}
	})
	if waiting {
		section(c, "确认配对")
		ui.Textf(c, "%s · %s", displayName(pending.Name, pending.PeerID), platformLabel(pending.Platform)).Bold()
		ui.Text(c, pending.Code).FontSize(32).Bold().FontFeatures("tnum")
		ui.Text(c, "请看另一台电脑：只有两边显示相同的校验码，才确认配对。确认完成前不会传送剪贴板或文件。").TextColor(t.TextMuted)
		ui.Textf(c, "剩余 %d 秒", max(0, int(time.Until(pending.Expires).Seconds())+1)).FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Children(func() {
			label := "校验码相同，确认配对"
			if pending.Confirmed {
				label = "等待另一台电脑确认"
			}
			if ui.PrimaryButton(c, label).Disabled(pending.Confirmed).Clicked() {
				a.engine.ConfirmPair(pending.Token, true)
			}
			if ui.Button(c, "拒绝配对").Disabled(pending.Confirmed).Clicked() {
				a.engine.ConfirmPair(pending.Token, false)
			}
		})
	}

	section(c, "已配对设备")
	link, up := a.engine.Link()
	if len(a.devices) == 0 {
		ui.Text(c, "还没有配对设备。点击「添加设备」，选择同一局域网内的电脑。").TextColor(t.TextMuted)
	}
	for _, dev := range a.devices {
		online := up && link.ID == dev.ID
		ui.Column(c.Key("paired-"+dev.ID)).Gap(6).Padding(10, 0).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, displayName(dev.Peer.Name, dev.ID)).Bold().SingleLine()
					state := "离线"
					if online {
						state = "已连接"
					} else if a.targetPeerID == dev.ID && a.dialCancel != nil {
						state = "正在重连"
					}
					ui.Text(c, platformLabel(dev.Peer.Platform)+" · "+state).FontSize(12).TextColor(t.TextMuted)
				})
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
				kv(c, "地址", dev.Peer.Addr)
				ui.Text(c, "证书指纹").FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, identity.FormatFingerprint(dev.Peer.Fingerprint)).Selectable().FontSize(12)
				if a.confirmRemove == dev.ID {
					ui.Text(c, "解除配对会立即断开连接。再次添加需要双方确认。").TextColor(t.TextMuted)
					ui.Row(c).Gap(8).Children(func() {
						if ui.Button(c, "确认解除配对").Clicked() {
							a.forget(dev.ID)
						}
						if ui.Button(c, "取消").Clicked() {
							a.confirmRemove = ""
						}
					})
				} else if ui.Button(c, "解除配对").Clicked() {
					a.confirmRemove = dev.ID
				}
			}
		})
	}

	section(c, "发现的设备")
	count := 0
	for _, dev := range a.discovered {
		if a.isPaired(dev.ID) {
			continue
		}
		count++
		ui.Row(c.Key("found-"+dev.ID)).Gap(8).Padding(8, 0).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, displayName(dev.Name, dev.ID)).SingleLine()
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
	if count == 0 {
		ui.Text(c, "正在寻找同一局域网内的电脑。请确认对端已打开 Snapover；找不到时可以手填地址。").TextColor(t.TextMuted)
	}
	if a.discoveryMessage != "" {
		ui.Text(c, a.discoveryMessage).FontSize(12).TextColor(t.TextMuted)
	}

	section(c, "手填地址")
	ui.Row(c).Gap(8).Children(func() {
		submit := ui.TextInput(c.Key("peer"), &a.peer).Placeholder("192.168.1.20，端口可省略").Grow(1).Submitted()
		if ui.Button(c, "连接地址").Disabled(waiting).Clicked() || (submit && !waiting) {
			a.targetPeerID = ""
			a.connect()
		}
	})
	ui.Text(c, "首次配对时，也需要在另一台电脑上点击「添加设备」。").FontSize(12).TextColor(t.TextMuted)
	if a.status != "" {
		ui.Text(c, a.status).TextColor(t.TextMuted)
	}
	if len(a.devices) > 0 {
		section(c, "解除全部配对")
		label := "解除全部配对"
		if a.confirm {
			label = "再次点击，解除全部配对"
			ui.Text(c, "这会立即断开连接。再次添加任何设备都需要双方确认。").TextColor(t.TextMuted)
		}
		if ui.Button(c, label).Clicked() {
			if !a.confirm {
				a.confirm = true
			} else {
				a.clearPairs()
			}
		}
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
