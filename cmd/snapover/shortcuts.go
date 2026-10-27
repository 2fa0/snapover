package main

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *app) windowShortcuts(c *ui.Context) {
	if a.shortcutRecording != nil || runtime.GOOS != "windows" {
		return
	}
	// Keep the menu's shortcuts available without a Windows menu bar.
	switch {
	case c.Shortcut(ui.Ctrl, ui.KeyComma):
		a.show(pageSettings)
	case c.Shortcut(ui.Ctrl, ui.Key2):
		a.show(pageDevices)
	case c.Shortcut(ui.Ctrl, ui.Key3):
		a.show(pageHistory)
	}
}

func normalizePickerShortcut(value string) (string, error) {
	parts := strings.Split(strings.TrimSpace(value), "+")
	mods := make(map[string]bool)
	for _, part := range parts[:len(parts)-1] {
		var mod string
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "cmd", "command", "super", "win", "meta":
			mod = "Super"
		case "ctrl", "control":
			mod = "Ctrl"
		case "cmdorctrl", "commandorcontrol":
			mod = "Ctrl"
			if runtime.GOOS == "darwin" {
				mod = "Super"
			}
		case "alt", "option":
			mod = "Alt"
		case "shift":
			mod = "Shift"
		default:
			return "", errors.New("修饰键请使用 Ctrl、Alt、Shift、Cmd 或 Win")
		}
		if mods[mod] {
			return "", errors.New("快捷键中有重复的修饰键")
		}
		mods[mod] = true
	}
	if !mods["Ctrl"] && !mods["Alt"] && !mods["Super"] {
		return "", errors.New("请至少使用 Ctrl、Alt、Cmd 或 Win 中的一个修饰键")
	}
	key := strings.ToUpper(strings.TrimSpace(parts[len(parts)-1]))
	valid := len(key) == 1 && (key[0] >= 'A' && key[0] <= 'Z' || key[0] >= '0' && key[0] <= '9')
	if strings.HasPrefix(key, "F") {
		n, err := strconv.Atoi(key[1:])
		valid = valid || err == nil && n >= 1 && n <= 12 && key == fmt.Sprintf("F%d", n)
	}
	for _, named := range []string{"Space", "Tab", "Enter", "Escape", "Backspace", "Delete", "Insert", "Home", "End", "PageUp", "PageDown", "Up", "Down", "Left", "Right"} {
		if strings.EqualFold(key, named) {
			key, valid = named, true
			break
		}
	}
	if !valid {
		return "", errors.New("主键请使用字母、数字、F1–F12、Space 或方向等常用键")
	}
	var result []string
	for _, mod := range []string{"Super", "Ctrl", "Alt", "Shift"} {
		if mods[mod] {
			result = append(result, mod)
		}
	}
	return strings.Join(append(result, key), "+"), nil
}

func configuredPickerShortcuts(value string) ([]string, error) {
	keys := pickerShortcuts()
	if strings.TrimSpace(value) != "" {
		keys = []string{value}
	}
	for i, key := range keys {
		normalized, err := normalizePickerShortcut(key)
		if err != nil {
			return nil, err
		}
		if normalized == "Ctrl+C" || normalized == "Super+C" {
			return nil, errors.New("请保留普通复制快捷键，另选一组组合键")
		}
		keys[i] = normalized
	}
	return keys, nil
}

// Keep existing keys live until new keys register and the setting is saved.
func changePickerShortcuts(current, wanted []string, register func(string) error, unregister func(string), save func() error) error {
	var added []string
	rollback := func() {
		for _, key := range added {
			unregister(key)
		}
	}
	for _, key := range wanted {
		if slices.Contains(current, key) {
			continue
		}
		if err := register(key); err != nil {
			rollback()
			return fmt.Errorf("无法注册 %s，可能被占用，请换一组快捷键后重试", displayPickerShortcut(key))
		}
		added = append(added, key)
	}
	if err := save(); err != nil {
		rollback()
		return errors.New("快捷键保存失败，请检查数据目录权限后重试")
	}
	for _, key := range current {
		if !slices.Contains(wanted, key) {
			unregister(key)
		}
	}
	return nil
}

func displayPickerShortcut(key string) string {
	mod := "Win"
	if runtime.GOOS == "darwin" {
		mod = "Cmd"
	}
	return strings.ReplaceAll(key, "Super", mod)
}

func (a *app) registerPickerShortcut() {
	keys, err := configuredPickerShortcuts(a.cfg.Values.PickerShortcut)
	if err != nil {
		a.shortcutError, a.status = err.Error(), err.Error()
		return
	}
	for _, key := range keys {
		if err := mygo.GlobalShortcut.Register(key, func() {
			if !a.captureRegisteredShortcut(key) {
				a.openPicker(true)
			}
		}); err != nil {
			a.shortcutError = "快捷键无法注册：" + displayPickerShortcut(key) + "。请在设置中更换，也可从托盘打开快速历史"
			a.status = a.shortcutError
		} else {
			a.registeredPickerShortcuts = append(a.registeredPickerShortcuts, key)
		}
	}
}

func (a *app) savePickerShortcut(value string) {
	keys, err := configuredPickerShortcuts(value)
	if err != nil {
		a.shortcutError = err.Error()
		return
	}
	for _, key := range keys {
		if slices.Contains(a.registeredCopyShortcuts, key) {
			a.shortcutError = "此快捷键已用于复制并同步，请另选一组"
			return
		}
	}
	previous := a.cfg.Values.PickerShortcut
	err = changePickerShortcuts(a.registeredPickerShortcuts, keys,
		func(key string) error {
			return mygo.GlobalShortcut.Register(key, func() {
				if !a.captureRegisteredShortcut(key) {
					a.openPicker(true)
				}
			})
		},
		mygo.GlobalShortcut.Unregister, func() error {
			a.cfg.Values.PickerShortcut = ""
			if strings.TrimSpace(value) != "" {
				a.cfg.Values.PickerShortcut = keys[0]
			}
			return a.cfg.Save()
		})
	if err != nil {
		a.cfg.Values.PickerShortcut = previous
		a.shortcutError = err.Error() + "。原快捷键保持不变"
		return
	}
	a.registeredPickerShortcuts, a.shortcutError = keys, ""
	a.shortcutText = displayPickerShortcut(keys[0])
	a.status = "快速历史快捷键已更新"
}

func (a *app) shortcutSettings(c *ui.Context) {
	a.shortcutRecorder(c, "picker", "快速历史快捷键", a.shortcutText, &a.shortcutError, a.savePickerShortcut)
}
