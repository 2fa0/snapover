//go:build darwin

package main

import (
	"errors"

	"github.com/2fa0/snapover/internal/sendto"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const sendToEntryName = "Finder 服务"

func refreshSendTo() error { return sendto.Refresh() }

func (a *app) initSendTo() {
	a.sendToColdLaunch = sendto.WasOpenedForService()
	cold := a.sendToColdLaunch
	if err := sendto.Register(func(paths []string) error {
		err := a.receiveFinderFiles(paths, cold)
		if err == nil {
			cold = false
		}
		return err
	}); err != nil {
		a.reportStatusError("注册 Finder 服务失败：" + err.Error())
		if a.engine.Log != nil {
			a.engine.Log.Error("注册 Finder 服务失败", "err", err)
		}
		return
	}
	if mygo.App.IsPackaged() {
		a.setSendTo(!a.cfg.Values.DisableSendTo, false)
	}
}

func (a *app) receiveFinderFiles(paths []string, cold bool) error {
	if a.quitting || a.cfg.Values.DisableSendTo {
		return errors.New("请在 Snapover 设置中启用 Finder 发送文件服务")
	}
	a.handleFileRequest(paths, cold)
	return nil
}

func (a *app) sendToSettings(c *ui.Context) {
	on := !a.cfg.Values.DisableSendTo
	if ui.Checkbox(c, &on, a.tr("允许从 Finder 发送文件")).
		Tooltip(a.tr("右键文件 → 服务 → Snapover 发送文件。菜单是否显示可在系统设置的「服务」中调整。")).Disabled(a.sendToBusy).Changed() {
		a.setSendTo(on, true)
	}
	if ui.Button(c, a.tr("刷新 Finder 服务")).Disabled(a.sendToBusy || !on).Clicked() {
		a.setSendTo(true, true)
	}
}
