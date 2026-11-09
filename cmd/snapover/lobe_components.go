package main

// Adapted from Lobe MyGo's SearchBar, Alert, Hotkey and InputNumber (MIT, Copyright 2026
// LobeHub). See licenses/lobe-mygo.txt for the source and license. Theme,
// localization and event handling use Snapover's existing implementations.

import (
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

var (
	clearSearchIcon = interfaceIcon("lobe-circle-x")
	searchLoadIcon  = interfaceIcon("lobe-loader-circle")
	infoIcon        = interfaceIcon("lobe-info")
	numberUpIcon    = interfaceIcon("lobe-chevron-up")
	numberDownIcon  = interfaceIcon("lobe-chevron-down")
)

// The explicit clear result lets callers reset selection and scroll just as
// they do on typing; changing the bound string does not emit Changed().
func (a *app) searchBar(c *ui.Context, query *string, label string, loading bool) (ui.Element, bool) {
	t := c.Theme()
	root := ui.Row(c).Grow(1).MinWidth(0).MinHeight(32).Padding(0, 12).Gap(8).
		Radius(t.Radius).AlignItems(ui.Center).FontSize(14).TextColor(t.Text).
		Cursor(ui.CursorText).Background(t.Surface).
		Transition(ui.ElementTransition{Duration: 150 * time.Millisecond, Colors: true, Shadows: true})
	var input ui.Element
	cleared := false
	root.Children(func() {
		if loading {
			icon := ui.Icon(c.Key("search-icon"), searchLoadIcon).FontSize(16).TextColor(t.TextMuted).Shrink(0)
			icon.Rotate(icon.Loop("spin", time.Second, ui.Linear) * 360)
		} else {
			ui.Icon(c.Key("search-icon"), searchIcon).FontSize(16).TextColor(t.TextMuted).Shrink(0)
		}
		input = ui.TextInputBase(c.Key("input"), query).Label(label).Grow(1).MinWidth(0).FocusRing(false).
			Placeholder(a.tr("搜索内容或来源")).PlaceholderColor(t.TextMuted)
		if *query != "" {
			clear := ui.ButtonBase(c.Key("clear")).Label(a.tr("清空搜索")).Tooltip(a.tr("清空搜索")).
				Size(24, 24).Radius(12).Center().Shrink(0).Cursor(ui.CursorPointer).TextColor(t.TextMuted)
			if clear.Hovered() {
				clear.Background(t.SurfaceHover).TextColor(t.Text)
			}
			if clear.Clicked() {
				*query = ""
				cleared = true
				input.Focus()
			}
			clear.Children(func() { ui.Icon(c, clearSearchIcon).FontSize(14) })
		}
	})
	root.Border(1, t.Border)
	if input.Focused() {
		root.Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.12))
	} else if root.Hovered() {
		root.Border(1, t.TextMuted)
	}
	return input, cleared
}

// Keep an integer string draft instead of InputNumber's float64 value: file
// limits must remain exact, and only the existing Apply/Enter actions save it.
func (a *app) numberInput(c *ui.Context, value *string, label string, min, max int64, width float32) ui.Element {
	t := c.Theme()
	root := ui.Row(c).Width(width).Shrink(0).MinHeight(32).Padding(0, 8).Gap(4).
		Radius(t.Radius).AlignItems(ui.Center).FontSize(14).Background(t.Surface)
	step := func(delta int64) {
		text := strings.TrimSpace(*value)
		if text == "" {
			*value = strconv.FormatInt(min, 10)
			return
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || n < min || n > max {
			return
		}
		if delta > 0 && n < max || delta < 0 && n > min {
			n += delta
		}
		*value = strconv.FormatInt(n, 10)
	}
	var input ui.Element
	root.Children(func() {
		input = ui.TextInputBase(c.Key("input"), value).Label(label).Grow(1).MinWidth(0).
			FocusRing(false).FontFeatures("tnum")
		if input.Shortcut(0, ui.KeyUp) {
			step(1)
		}
		if input.Shortcut(0, ui.KeyDown) {
			step(-1)
		}
		ui.Column(c).Shrink(0).Children(func() {
			for _, control := range []struct {
				label string
				icon  *ui.SVG
				delta int64
			}{{a.tr("增加") + " " + label, numberUpIcon, 1}, {a.tr("减少") + " " + label, numberDownIcon, -1}} {
				b := ui.ButtonBase(c.Key(control.delta)).Label(control.label).Size(20, 14).Radius(3).
					Center().Cursor(ui.CursorPointer).TextColor(t.TextMuted)
				if b.Hovered() {
					b.Background(t.SurfaceHover).TextColor(t.Text)
				}
				if b.Clicked() {
					step(control.delta)
					input.Focus()
				}
				b.Children(func() { ui.Icon(c, control.icon).FontSize(12) })
			}
		})
	})
	border := t.Border
	if input.Focused() {
		border = t.Accent
		root.Shadow(0, 0, 0, 2, t.Accent.Alpha(0.12))
	}
	if text := strings.TrimSpace(*value); text != "" {
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || n < min || n > max {
			border = t.Danger
		}
	}
	root.Border(1, border)
	return input
}

func alertMessage(c *ui.Context, title, description string, icon *ui.SVG, tone ui.Color) ui.Element {
	t := c.Theme()
	return ui.Row(c).Gap(10).AlignSelf(ui.Stretch).AlignItems(ui.Start).Radius(t.Radius).
		Padding(12, 14).FontSize(13).TextColor(t.Text).Background(tone.Alpha(0.05)).
		InsetShadow(0, 0, 0, 1, tone.Alpha(0.14)).Children(func() {
		ui.Row(c).Height(20).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Icon(c, icon).FontSize(18).TextColor(tone)
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, title).FontWeight(500).LineHeight(20.0 / 13)
			if description != "" {
				ui.Text(c, description).FontSize(12).LineHeight(20.0 / 12).TextColor(t.TextMuted)
			}
		})
	})
}

func errorMessage(c *ui.Context, message string) {
	alertMessage(c, message, "", clearSearchIcon, c.Theme().Danger).Role(ui.RoleAlert).Label(message)
}

// Keys are already formatted for the current platform by the callers.
func hotkeyCaps(c *ui.Context, keys string) ui.Element {
	t := c.Theme()
	return ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		for i, key := range strings.Split(keys, "+") {
			ui.Row(c.Key(i)).AlignItems(ui.Center).Justify(ui.Center).Height(22).MinWidth(22).
				Padding(0, 6).Radius(4).FontSize(11).LineHeight(1.1).
				TextColor(t.TextMuted).Background(t.SurfaceHover).Children(func() {
				ui.Text(c, key).SingleLine()
			})
		}
	})
}
