package main

func (a *app) setKeepRemoteActive(on bool) {
	old := a.cfg.Values.KeepRemoteActive
	// Disabling takes effect even if saving the preference fails.
	if !on {
		a.remoteActivity.SetEnabled(false)
	}
	a.cfg.Values.KeepRemoteActive = on
	if err := a.cfg.Save(); err != nil {
		if on {
			a.cfg.Values.KeepRemoteActive = old
			a.status = "保存远程会话设置失败，请重试"
		} else {
			a.status = "已停止保持远程会话活跃，但设置保存失败；重启前请重试保存"
		}
		return
	}
	a.remoteActivity.SetEnabled(on)
	if on {
		a.status = "已开启保持远程会话活跃"
	} else {
		a.status = "已关闭保持远程会话活跃"
	}
}
