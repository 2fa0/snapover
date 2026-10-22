package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/keepawake"
)

type keepAwakeSession interface {
	Stop()
	Status() keepawake.Status
}

func (a *app) keepAwakeSettings(c *ui.Context) {
	if settingSwitch(c, &a.keepAwake, "防止自动锁屏", "保持屏幕亮着，并在空闲时尝试阻止自动锁屏。关闭窗口后继续运行，会增加耗电。") {
		a.setKeepAwake(a.keepAwake)
	}
	message := a.keepAwakeError
	if a.keepAwakeWorker != nil {
		state := a.keepAwakeWorker.Status()
		if state.Err != nil {
			message = "防止自动锁屏已停止：" + state.Err.Error() + "。请关闭开关后重新开启。"
		} else if message == "" {
			text := "已请求保持唤醒，自动锁屏效果取决于系统策略。手动锁屏和主动睡眠仍可使用。"
			if state.Paused {
				text = "已暂停，返回当前用户桌面后恢复。"
			}
			ui.Text(c, text).FontSize(12).TextColor(c.Theme().TextMuted)
		}
		if state.Err == nil {
			c.After(time.Second)
		}
	}
	if message != "" {
		ui.Text(c, message).FontSize(12).TextColor(c.Theme().Danger)
	}
}

func (a *app) setKeepAwake(on bool) {
	if !on {
		// Stopping the current request must not depend on writable settings.
		a.stopKeepAwake()
		a.keepAwake = false
		a.cfg.Values.KeepAwake = false
		a.keepAwakeError = ""
		if err := a.cfg.Save(); err != nil {
			a.keepAwakeError = "已停止保持唤醒，但关闭状态未保存，下次启动可能重新开启。请检查数据目录权限。"
		}
		return
	}
	previous := a.cfg.Values.KeepAwake
	worker, err := keepawake.Start(a.root, a.engine.Log)
	if err != nil {
		a.keepAwake = previous
		a.keepAwakeError = "防止自动锁屏未开启：" + err.Error()
		if a.engine.Log != nil {
			a.engine.Log.Warn("防止自动锁屏未开启", "err", err)
		}
		return
	}
	a.cfg.Values.KeepAwake = true
	if err := a.cfg.Save(); err != nil {
		worker.Stop()
		a.cfg.Values.KeepAwake = previous
		a.keepAwake = previous
		a.keepAwakeError = "保持唤醒设置保存失败，设置未更改，请检查数据目录权限后重试。"
		return
	}
	a.stopKeepAwake()
	a.keepAwakeWorker = worker
	a.keepAwake = true
	a.keepAwakeError = ""
}

func (a *app) restoreKeepAwake() {
	if !keepawake.Supported || !a.keepAwake {
		return
	}
	worker, err := keepawake.Start(a.root, a.engine.Log)
	if err != nil {
		a.keepAwakeError = "防止自动锁屏未开启：" + err.Error() + "。请关闭开关后重新开启。"
		if a.engine.Log != nil {
			a.engine.Log.Warn("防止自动锁屏未开启", "err", err)
		}
		return
	}
	a.keepAwakeWorker = worker
}

func (a *app) stopKeepAwake() {
	if a.keepAwakeWorker != nil {
		a.keepAwakeWorker.Stop()
		a.keepAwakeWorker = nil
	}
}
