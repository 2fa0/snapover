package main

import (
	"errors"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func configuredCopyShortcut(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		value = "Ctrl+Alt+C"
		if runtime.GOOS == "darwin" {
			value = "Super+Alt+C"
		}
	}
	key, err := normalizePickerShortcut(value)
	if err != nil {
		return "", err
	}
	if key == "Ctrl+C" || key == "Super+C" {
		return "", errors.New("请保留普通复制快捷键，另选一组组合键")
	}
	return key, nil
}

func (a *app) registerCopyShortcut() {
	key, err := configuredCopyShortcut(a.cfg.Values.CopyShortcut)
	if err == nil {
		err = a.registerCopyKey(key)
	}
	if err != nil {
		a.copyShortcutError = err.Error()
		a.status = a.copyShortcutError
		return
	}
	a.registeredCopyShortcuts = []string{key}
}

func (a *app) registerCopyKey(key string) error {
	if slices.Contains(a.registeredPickerShortcuts, key) {
		return errors.New("此快捷键已用于快速历史，请另选一组")
	}
	if err := mygo.GlobalShortcut.Register(key, func() {
		if !a.captureRegisteredShortcut(key) {
			a.startCopySync()
		}
	}); err != nil {
		return errors.New("复制并同步快捷键无法注册，请在设置中更换")
	}
	return nil
}

func (a *app) saveCopyShortcut(value string) {
	key, err := configuredCopyShortcut(value)
	if err != nil {
		a.copyShortcutError = err.Error()
		return
	}
	previous := a.cfg.Values.CopyShortcut
	err = changePickerShortcuts(a.registeredCopyShortcuts, []string{key}, a.registerCopyKey,
		mygo.GlobalShortcut.Unregister, func() error {
			a.cfg.Values.CopyShortcut = ""
			if strings.TrimSpace(value) != "" {
				a.cfg.Values.CopyShortcut = key
			}
			return a.cfg.Save()
		})
	if err != nil {
		a.cfg.Values.CopyShortcut = previous
		a.copyShortcutError = err.Error() + "。原快捷键保持不变"
		return
	}
	a.registeredCopyShortcuts = []string{key}
	a.copyShortcutText, a.copyShortcutError = displayPickerShortcut(key), ""
	a.status = "复制并同步快捷键已更新"
}

func (a *app) copyShortcutSettings(c *ui.Context) {
	a.shortcutRecorder(c, "copy", "复制并同步快捷键", a.copyShortcutText, &a.copyShortcutError, a.saveCopyShortcut)
}
