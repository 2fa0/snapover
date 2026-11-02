package main

import (
	"github.com/egoist/mygo/ui"
)

const sendToEntryName = "Windows「发送到」入口"

func refreshSendTo() error { return nil }

func (a *app) initSendTo() { a.setSendTo(!a.cfg.Values.DisableSendTo, false) }

func (a *app) sendToSettings(c *ui.Context) {
	on := !a.cfg.Values.DisableSendTo
	if ui.Checkbox(c, &on, a.tr("在 Windows「发送到」菜单中显示 Snapover")).Disabled(a.sendToBusy).Changed() {
		a.setSendTo(on, true)
	}
}
