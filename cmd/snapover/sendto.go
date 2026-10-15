//go:build darwin || windows

package main

import (
	"context"
	"time"

	"github.com/2fa0/snapover/internal/sendto"
)

// Both shell integrations use the same install, persistence and rollback flow.
// Only their settings UI and service refresh are platform-specific.
func (a *app) setSendTo(on, save bool) {
	if a.sendToBusy {
		return
	}
	a.sendToBusy = true
	root := a.root
	if root == nil {
		root = context.Background()
	}
	go func() {
		ctx, cancel := context.WithTimeout(root, 10*time.Second)
		defer cancel()
		var err error
		if on {
			err = sendto.Install(ctx)
		} else {
			err = sendto.Remove()
		}
		a.updateFiles(func() {
			if err == nil {
				err = refreshSendTo()
			}
			a.sendToBusy = false
			if err != nil {
				if a.engine.Log != nil {
					a.engine.Log.Warn("更新 "+sendToEntryName+" 失败", "enabled", on, "err", err)
				}
				a.status = "更新 " + sendToEntryName + " 失败：" + err.Error() + "。请在设置中重新启用"
				return
			}
			if save {
				old := a.cfg.Values.DisableSendTo
				a.cfg.Values.DisableSendTo = !on
				if err := a.cfg.Save(); err != nil {
					a.cfg.Values.DisableSendTo = old
					a.setSendTo(!old, false)
					a.status = "保存 " + sendToEntryName + " 设置失败，请重试"
					return
				}
				if on {
					a.status = "已启用 " + sendToEntryName
				} else {
					a.status = "已停用 " + sendToEntryName
				}
			}
		})
	}()
}
