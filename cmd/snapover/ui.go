package main

import (
	"embed"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/resources"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
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
	fileIcon     = interfaceIcon("document")
	imageIcon    = interfaceIcon("photo")
	folderIcon   = interfaceIcon("folder")
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

func settingsGroup(c *ui.Context, title string, contents func()) ui.Element {
	return ui.Column(c.Key(title)).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(13).Bold()
		ui.Column(c).Padding(16).Gap(14).Radius(12).Background(c.Theme().Surface).Children(contents)
	})
}

func settingSwitch(c *ui.Context, value *bool, title, description string) bool {
	changed := false
	switchLabel := title + "开关"
	if title != "" && title[0] < 0x80 {
		switchLabel = title + " switch"
	}
	ui.Row(c.Key(title)).Gap(20).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, title).Bold()
			if description != "" {
				ui.Text(c, description).FontSize(12).TextColor(c.Theme().TextMuted)
			}
		})
		changed = ui.Switch(c, value).Label(switchLabel).Changed()
	})
	return changed
}

func emptyState(c *ui.Context, icon *ui.SVG, title, description string) {
	arrive(ui.Column(c).Padding(24, 16).Gap(8).AlignItems(ui.Center)).Children(func() {
		ui.Icon(c, icon).FontSize(28).TextColor(c.Theme().TextMuted)
		ui.Text(c, title).Bold()
		if description != "" {
			ui.Text(c, description).TextColor(c.Theme().TextMuted).FontSize(12)
		}
	})
}

// mainTitleBarHeight is the strip at the top of the window that the window
// controls sit in and that drags the window (Linux and Windows; macOS places
// the controls by TrafficLightPosition and measures the strip itself).
const mainTitleBarHeight = 40

// sidebarLogoSize is the DIPs the bird in the sidebar header is drawn at: big
// enough for the eye and the beak of the artwork to read.
const sidebarLogoSize = 28

// The sidebar's rows carry three items, so they can afford the room the
// widget's default rows spend on sections and badges: taller than the text and
// apart from each other, which tells three choices from one block of text.
const (
	sidebarItemPadding = 12
	sidebarItemGap     = 6
)

// sidebarNavItem is one page of the main sidebar.
func sidebarNavItem(c *ui.Context, page string, icon *ui.SVG, label string) {
	ui.SidebarItem(c, page, icon, label).
		Padding(sidebarItemPadding, 8).
		Margin(0, 0, sidebarItemGap, 0).
		Transition(navMotion)
}

// sidebarLogo is the bird in the sidebar header, a picture of the bird the app
// icon carries, decoded once and kept for the life of the window.
func (a *app) sidebarLogo() *ui.Bitmap {
	if a.logo == nil {
		bmp, err := ui.DecodeBitmap(resources.LogoPNG)
		if err != nil {
			return nil
		}
		a.logo = bmp
	}
	return a.logo
}

func (a *app) historyThumb(c *ui.Context, entry history.Entry, t *ui.Theme, selected bool, size float32) {
	if entry.Kind == string(clip.KindImage) {
		if bmp := a.thumb(entry.ID); bmp != nil {
			ui.Image(c, bmp).Size(size, size).Fit(ui.Contain).Radius(6)
			return
		}
	}
	// Filled shapes mark the items that name something on disk (files,
	// folders, images), while text keeps the outline page with its lines: the
	// fill is what tells them apart at 20 DIPs, where the line detail is lost.
	icon := historyKindIcon(entry)
	ui.Column(c).Size(size, size).Radius(6).Background(rowThumbPlaceholder(t, selected)).
		AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Icon(c, icon).FontSize(size * 0.5).TextColor(rowMetaColor(t, selected))
	})
}

func historyKindIcon(entry history.Entry) *ui.SVG {
	switch {
	case entry.Kind == string(clip.KindImage):
		return imageIcon
	case entry.Kind == string(clip.KindFiles) && entry.Folder:
		return folderIcon
	case entry.Kind == string(clip.KindFiles):
		return fileIcon
	default:
		return textIcon
	}
}

func (a *app) historySource(c *ui.Context, e history.Entry, color ui.Color, fontSize float32) {
	ui.Row(c).Grow(1).MinWidth(0).Gap(5).AlignItems(ui.Center).Children(func() {
		label := a.tr("本机")
		if e.Dir == history.DirRemote {
			label = a.tr("同步")
		}
		ui.Text(c, label).FontSize(fontSize).TextColor(color).SingleLine().Shrink(0)
		if e.Dir == history.DirRemote {
			peer := a.displayName(e.Peer, a.tr("对端"))
			ui.Text(c, peer).FontSize(fontSize).TextColor(color).SingleLine().MinWidth(fontSize * 2).Tooltip(peer)
			if e.SourceApp != "" {
				ui.Text(c, "·").FontSize(fontSize).TextColor(color).Shrink(0)
			}
		}
		if e.SourceApp != "" {
			ui.Text(c, e.SourceApp).FontSize(fontSize).TextColor(color).SingleLine().MinWidth(fontSize * 2).Tooltip(e.SourceApp)
		}
	})
}
