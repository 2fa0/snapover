//go:build darwin

package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/sendto"
)

const sendToEntryName = "Finder 快速操作"

func refreshSendTo() error { return sendto.Refresh() }

func (a *app) initSendTo() {
	if mygo.App.IsPackaged() {
		a.setSendTo(!a.cfg.Values.DisableSendTo, false)
	}
}

func (a *app) sendToSettings(c *ui.Context) {
	on := !a.cfg.Values.DisableSendTo
	if ui.Checkbox(c, &on, "在 Finder「快速操作」中显示 Snapover").
		Tooltip("右键文件 → 快速操作 → Snapover 发送文件。未显示时，在系统设置中启用该快速操作。").Disabled(a.sendToBusy).Changed() {
		a.setSendTo(on, true)
	}
	if ui.Button(c, "重新安装快速操作").Disabled(a.sendToBusy || !on).Clicked() {
		a.setSendTo(true, true)
	}
}
