package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type shortcutRecording struct {
	kind string
	key  string
	menu *mygo.Menu
}

func (a *app) beginShortcutRecording(kind string) {
	a.cancelShortcutRecording()
	r := &shortcutRecording{kind: kind}
	if a.win != nil {
		// Menu accelerators otherwise consume chords before the recorder sees them.
		r.menu = mygo.App.Menu()
		mygo.App.SetMenu(nil)
	}
	a.shortcutRecording = r
	if kind == "copy" {
		a.copyShortcutError = ""
	} else {
		a.shortcutError = ""
	}
}

func (a *app) cancelShortcutRecording() {
	if r := a.shortcutRecording; r != nil {
		a.shortcutRecording = nil
		if a.win != nil {
			mygo.App.SetMenu(r.menu)
		}
	}
}

// Registered global shortcuts may consume native key events. While recording,
// use their known chord as input and leave the original action dormant.
func (a *app) captureRegisteredShortcut(key string) bool {
	if a.shortcutRecording == nil {
		return false
	}
	a.recordShortcut(key)
	if a.win != nil {
		a.win.Invalidate()
	}
	return true
}

func (a *app) recordShortcut(key string) {
	r := a.shortcutRecording
	if r == nil {
		return
	}
	var err error
	if r.kind == "copy" {
		_, err = configuredCopyShortcut(key)
		a.copyShortcutError = ""
		if err != nil {
			a.copyShortcutError = err.Error()
		}
	} else {
		_, err = configuredPickerShortcuts(key)
		a.shortcutError = ""
		if err != nil {
			a.shortcutError = err.Error()
		}
	}
	r.key = ""
	if err == nil {
		r.key = key
	}
}

func shortcutFromInput(ev ui.InputEvent) (string, error) {
	key := ev.Key
	if ev.PhysicalKey != ui.KeyUnknown {
		key = ev.PhysicalKey
	}
	var name string
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		name = string(rune('A' + key - ui.KeyA))
	case key >= ui.Key0 && key <= ui.Key9:
		name = string(rune('0' + key - ui.Key0))
	case key >= ui.KeyF1 && key <= ui.KeyF12:
		name = fmt.Sprintf("F%d", key-ui.KeyF1+1)
	default:
		name = map[ui.Key]string{
			ui.KeySpace: "Space", ui.KeyTab: "Tab", ui.KeyEnter: "Enter", ui.KeyEscape: "Escape",
			ui.KeyBackspace: "Backspace", ui.KeyDelete: "Delete", ui.KeyInsert: "Insert",
			ui.KeyHome: "Home", ui.KeyEnd: "End", ui.KeyPageUp: "PageUp", ui.KeyPageDown: "PageDown",
			ui.KeyUp: "Up", ui.KeyDown: "Down", ui.KeyLeft: "Left", ui.KeyRight: "Right",
		}[key]
	}
	if name == "" {
		return "", fmt.Errorf("请使用字母、数字、F1–F12 或方向等常用键")
	}
	var parts []string
	for _, mod := range []struct {
		flag ui.Modifiers
		name string
	}{
		{ui.Super, "Super"}, {ui.Ctrl, "Ctrl"}, {ui.Alt, "Alt"}, {ui.Shift, "Shift"},
	} {
		if ev.Mods&mod.flag != 0 {
			parts = append(parts, mod.name)
		}
	}
	return normalizePickerShortcut(strings.Join(append(parts, name), "+"))
}

func (a *app) shortcutRecorderInput(ev ui.InputEvent) bool {
	r := a.shortcutRecording
	if r == nil {
		return false
	}
	switch ev.Kind {
	case ui.InputKeyDown:
		if ev.Key == ui.KeyEscape && ev.Mods == 0 {
			a.cancelShortcutRecording()
			return true
		}
		if ev.Key == ui.KeyTab && ev.Mods&(ui.Ctrl|ui.Alt|ui.Super) == 0 {
			return false // Keep Save and Cancel reachable with Tab.
		}
		if ev.Repeat || ev.Key == ui.KeyUnknown && ev.PhysicalKey == ui.KeyUnknown {
			return true
		}
		key, err := shortcutFromInput(ev)
		if err != nil {
			r.key = ""
			if r.kind == "copy" {
				a.copyShortcutError = err.Error()
			} else {
				a.shortcutError = err.Error()
			}
		} else {
			a.recordShortcut(key)
		}
		return true
	case ui.InputText, ui.InputCompose, ui.InputCommand:
		return true
	}
	return false
}

func (a *app) shortcutRecorder(c *ui.Context, kind, title, current string, failure *string, save func(string)) {
	row := ui.Column(c).Gap(8)
	hasFocus := false
	row.Children(func() {
		r := a.shortcutRecording
		active := r != nil && r.kind == kind
		label := current
		if active {
			label = a.tr("请按下快捷键…")
			if r.key != "" {
				label = displayPickerShortcut(r.key)
			}
		}
		settingRow(c, a.tr(title), "", func() {
			button := ui.Button(c.Key("record"), "").Label(a.tr("设置" + title))
			button.Children(func() {
				if active && r.key == "" {
					ui.Text(c, label).TextColor(c.Theme().TextMuted).SingleLine()
				} else {
					hotkeyCaps(c, label)
				}
			})
			if button.Clicked() {
				a.beginShortcutRecording(kind)
				button.Focus()
				r, active = a.shortcutRecording, true
			}
			hasFocus = button.Focused()
			if active {
				button.HandleInput(a.shortcutRecorderInput)
			} else if ui.Button(c, a.tr("恢复默认")).Label(a.tr("恢复默认" + title)).Clicked() {
				save("")
			}
		})
		if active {
			ui.Row(c).Gap(8).Children(func() {
				saveButton := ui.Button(c, a.tr("保存")).Label(a.tr("保存" + title)).Disabled(r.key == "")
				hasFocus = hasFocus || saveButton.Focused()
				if saveButton.Clicked() {
					save(r.key)
					if *failure == "" {
						a.cancelShortcutRecording()
					}
				}
				cancelButton := ui.Button(c, a.tr("取消")).Label(a.tr("取消设置" + title))
				hasFocus = hasFocus || cancelButton.Focused()
				if cancelButton.Clicked() {
					a.cancelShortcutRecording()
				}
			})
		}
	})
	if r := a.shortcutRecording; r != nil && r.kind == kind {
		if !hasFocus || row.Shortcut(0, ui.KeyEscape) {
			a.cancelShortcutRecording()
		}
	}
	if *failure != "" {
		errorMessage(c, a.tr(*failure))
	}
}
