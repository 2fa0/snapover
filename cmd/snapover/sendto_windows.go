package main

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/sendto"
)

func (a *app) initSendTo() { a.setSendTo(!a.cfg.Values.DisableSendTo, false) }

func (a *app) sendToSettings(c *ui.Context) {
	on := !a.cfg.Values.DisableSendTo
	if ui.Checkbox(c, &on, "在 Windows「发送到」菜单中显示 Snapover").Disabled(a.sendToBusy).Changed() {
		a.setSendTo(on, true)
	}
}

func (a *app) setSendTo(on, save bool) {
	if a.sendToBusy {
		return
	}
	a.sendToBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(a.root, 10*time.Second)
		defer cancel()
		var err error
		if on {
			err = sendto.Install(ctx)
		} else {
			err = sendto.Remove()
		}
		if err != nil && a.engine.Log != nil {
			a.engine.Log.Warn("更新「发送到」入口失败", "enabled", on, "err", err)
		}
		a.updateFiles(func() {
			a.sendToBusy = false
			if err != nil {
				a.status = "更新「发送到」入口失败：" + err.Error() + "。请检查权限后重试"
				return
			}
			if save {
				old := a.cfg.Values.DisableSendTo
				a.cfg.Values.DisableSendTo = !on
				if err := a.cfg.Save(); err != nil {
					a.cfg.Values.DisableSendTo = old
					a.setSendTo(!old, false)
					a.status = "保存「发送到」设置失败，请重试"
					return
				}
				if on {
					a.status = "已添加到 Windows「发送到」菜单"
				} else {
					a.status = "已移除 Windows「发送到」入口"
				}
			}
		})
	}()
}
