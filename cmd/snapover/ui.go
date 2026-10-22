package main

import (
	"embed"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
)

// Heroicons, MIT licensed; see icons/LICENSE.
//
//go:embed icons/*.svg
var interfaceIcons embed.FS

func interfaceIcon(name string) *ui.SVG {
	data, err := interfaceIcons.ReadFile("icons/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(data)
}

var (
	settingsIcon = interfaceIcon("adjustments-horizontal")
	deviceIcon   = interfaceIcon("computer-desktop")
	historyIcon  = interfaceIcon("clipboard-document-list")
	textIcon     = interfaceIcon("document-text")
	imageIcon    = interfaceIcon("photo")
	sendIcon     = interfaceIcon("arrow-up-tray")
	pinIcon      = interfaceIcon("bookmark")
	searchIcon   = interfaceIcon("magnifying-glass")
)

var (
	pageAnimation = ui.Animation{Spring: ui.SpringOptions{Duration: 180 * time.Millisecond}}
	pageMotion    = ui.ElementTransition{Animation: &pageAnimation, Enter: &ui.Motion{Y: 4}}
	navMotion     = ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true}
)

func appVersionLabel() string {
	if version := mygo.App.Version(); version != "" {
		return version
	}
	return "开发版"
}

func settingsGroup(c *ui.Context, title string, contents func()) {
	ui.Column(c.Key(title)).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(13).Bold()
		ui.Column(c).Padding(16).Gap(14).Radius(12).Background(c.Theme().Surface).Children(contents)
	})
}

func settingSwitch(c *ui.Context, value *bool, title, description string) bool {
	changed := false
	ui.Row(c.Key(title)).Gap(20).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, title).Bold()
			if description != "" {
				ui.Text(c, description).FontSize(12).TextColor(c.Theme().TextMuted)
			}
		})
		changed = ui.Switch(c, value).Label(title + "开关").Changed()
	})
	return changed
}

func emptyState(c *ui.Context, icon *ui.SVG, title, description string) {
	ui.Column(c).Padding(24, 16).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon).FontSize(28).TextColor(c.Theme().TextMuted)
		ui.Text(c, title).Bold()
		if description != "" {
			ui.Text(c, description).TextColor(c.Theme().TextMuted).FontSize(12)
		}
	})
}

func (a *app) historyThumb(c *ui.Context, entry history.Entry, t *ui.Theme, selected bool, size float32) {
	if entry.Kind == string(clip.KindImage) {
		if bmp := a.thumb(entry.ID); bmp != nil {
			ui.Image(c, bmp).Size(size, size).Fit(ui.Contain).Radius(6)
			return
		}
	}
	icon := textIcon
	if entry.Kind == string(clip.KindImage) {
		icon = imageIcon
	}
	ui.Column(c).Size(size, size).Radius(6).Background(rowThumbPlaceholder(t, selected)).
		AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Icon(c, icon).FontSize(size * 0.5).TextColor(rowMetaColor(t, selected))
	})
}

func historySource(e history.Entry) string {
	if e.Dir == history.DirRemote {
		source := "来自 " + displayName(e.Peer, "对端")
		if e.SourceApp != "" {
			source = e.SourceApp + " · " + source
		}
		return source
	}
	if e.SourceApp != "" {
		return e.SourceApp
	}
	return "本机"
}
