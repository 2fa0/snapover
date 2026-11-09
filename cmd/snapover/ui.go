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

// Heroicons (MIT) and selected Lucide icons (ISC/MIT); see icons/LICENSE*.
// Adapted Lobe MyGo component notices are also included in the application.
//
//go:embed icons/*.svg icons/LICENSE* licenses/*.txt
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
	t := c.Theme()
	return ui.Column(c.Key(title)).AlignSelf(ui.Stretch).Radius(12).Border(1, t.Border).Background(t.Surface).Children(func() {
		ui.Text(c, title).FontSize(16).FontWeight(600).Padding(16)
		ui.Divider(c)
		ui.Column(c).Padding(16).Gap(14).Grow(1).MinWidth(0).Children(contents)
	})
}

// Adapted from Lobe MyGo's Form.Field; persistence stays with each caller.
func settingRow(c *ui.Context, title, description string, controls func()) ui.Element {
	return ui.Row(c.Key(title)).Gap(12).AlignItems(ui.Center).AlignSelf(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, title).FontSize(14).FontWeight(500)
			if description != "" {
				ui.Text(c, description).FontSize(12).LineHeight(1.44).TextColor(c.Theme().TextMuted)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(controls)
	})
}

func settingSwitch(c *ui.Context, value *bool, title, description string) bool {
	changed := false
	switchLabel := title + "开关"
	if title != "" && title[0] < 0x80 {
		switchLabel = title + " switch"
	}
	settingRow(c, title, description, func() {
		changed = ui.Switch(c, value).Label(switchLabel).Changed()
	})
	return changed
}

// Adapted from Lobe MyGo's Empty, including its optional action area.
func emptyState(c *ui.Context, icon *ui.SVG, title, description string, action func()) {
	t := c.Theme()
	arrive(ui.Column(c).Gap(4).Padding(24, 8).AlignItems(ui.Center).FillWidth()).Children(func() {
		ui.Icon(c, icon).FontSize(32).TextColor(t.TextMuted)
		ui.Text(c, title).FontWeight(500).TextAlign(ui.Center).Margin(8, 0, 0, 0)
		if description != "" {
			ui.Text(c, description).FontSize(13).TextColor(t.TextMuted).TextAlign(ui.Center).MaxWidth(13 * 0.55 * 34)
		}
		if action != nil {
			ui.Row(c).Gap(8).Margin(8, 0, 0, 0).Children(action)
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
