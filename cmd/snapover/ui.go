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
	removeIcon   = interfaceIcon("lobe-x")
	chevronIcon  = interfaceIcon("lobe-chevron-down")
	helpIcon     = interfaceIcon("question-mark-circle")
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
		cardDivider(c)
		ui.Column(c).Padding(16).Gap(14).Grow(1).MinWidth(0).Children(contents)
	})
}

// cardDivider separates the rows of a card. The card's own border already
// outlines the group, so the lines between rows sit most of the way toward the
// card's background and leave the labels the strongest thing in it.
func cardDivider(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Box(c).Height(1).Shrink(0).AlignSelf(ui.Stretch).Background(t.Border.Mix(t.Surface, 0.7))
}

// pageScroll scrolls a page that is taller than the window. Only the window's
// side edges are left to the scroll, so the bar runs along the window's edge
// and covers no card; the page's own padding sits inside it.
func pageScroll(c *ui.Context, contents func()) ui.Element {
	return ui.Column(c).Grow(1).FillWidth().Padding(20, 0).Children(func() {
		ui.Scroll(c).Grow(1).ScrollbarInsets(0, 2, 0, 0).Children(func() {
			ui.Column(c).FillWidth().Gap(20).Padding(0, 28, 28, 28).Children(contents)
		})
	})
}

// Adapted from Lobe MyGo's Form.Field; persistence stays with each caller.
func settingRow(c *ui.Context, title, description string, controls func()) ui.Element {
	return settingRowHelp(c, title, description, "", controls)
}

// settingRowHelp is settingRow with a question mark after the title, which a
// pointer over it explains while the row itself keeps saying only its name.
func settingRowHelp(c *ui.Context, title, description, help string, controls func()) ui.Element {
	return ui.Row(c.Key(title)).Gap(12).AlignItems(ui.Center).AlignSelf(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(6).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(14).FontWeight(500)
				if help != "" {
					settingHelp(c, help)
				}
			})
			if description != "" {
				ui.Text(c, description).FontSize(12).LineHeight(1.44).TextColor(c.Theme().TextMuted)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(controls)
	})
}

// settingHelp is the question mark that explains a setting on hover: small and
// muted enough to stay quiet beside the title, with a pointer of its own.
func settingHelp(c *ui.Context, text string) ui.Element {
	t := c.Theme()
	mark := ui.Icon(c, helpIcon).FontSize(14).Padding(2).Shrink(0).
		Cursor(ui.CursorHelp).Tooltip(text).TextColor(t.TextMuted)
	if mark.Hovered() {
		mark.TextColor(t.Text)
	}
	return mark
}

func settingSwitch(c *ui.Context, value *bool, title, description string) bool {
	changed := false
	settingRow(c, title, description, func() {
		changed = ui.Switch(c, value).Label(switchLabel(title)).Changed()
	})
	return changed
}

// settingSwitchHelp is settingSwitch with a question mark that explains it, and
// the explanation also read out as the switch's description.
func settingSwitchHelp(c *ui.Context, value *bool, title, help string) bool {
	changed := false
	settingRowHelp(c, title, "", help, func() {
		changed = ui.Switch(c, value).Label(switchLabel(title)).Description(help).Changed()
	})
	return changed
}

// settingDisclosure is a settings section that opens and closes. Its header
// reads as the rows beside it rather than a level below them: the title at a
// setting's size, at the row's left edge, with the arrow at the far end.
func settingDisclosure(c *ui.Context, title string, open *bool, contents func()) ui.Element {
	t := c.Theme()
	return ui.Column(c.Key(title)).AlignSelf(ui.Stretch).Children(func() {
		p := ui.CollapsibleBase(c.Key("section"), open)
		tr := p.Trigger.AlignSelf(ui.Stretch).AlignItems(ui.Center).Gap(12).
			Padding(3, 0).Radius(t.Radius)
		if tr.Hovered() {
			tr.Background(t.SurfaceHover)
		}
		tr.Children(func() {
			ui.Text(c, title).FontSize(14).FontWeight(500).Grow(1).MinWidth(0).SingleLine()
			ui.Icon(c, chevronIcon).FontSize(16).TextColor(t.TextMuted).Shrink(0).
				Rotate(180 * p.Progress())
		})
		if panel := p.Panel(contents); panel.Valid() {
			panel.Padding(8, 0)
		}
	})
}

// switchLabel names a switch for assistive technology: a title of ASCII, as
// app names usually are, takes the English word.
func switchLabel(title string) string {
	if title != "" && title[0] < 0x80 {
		return title + " switch"
	}
	return title + "开关"
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
		// An image whose preview is still being decoded holds its square
		// with the placeholder rather than jump to a picture later.
		if a.thumbLoading(entry.ID) {
			skeleton(c, size, size, 6)
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

// Most rows are local copies, so the row says nothing about them: only a
// synced row is marked, and the source application is named when there is one.
func (a *app) historySource(c *ui.Context, e history.Entry, color ui.Color, fontSize float32) {
	remote := e.Dir == history.DirRemote
	if !remote && e.SourceApp == "" {
		return
	}
	ui.Row(c).Grow(1).MinWidth(0).Gap(5).AlignItems(ui.Center).Children(func() {
		if remote {
			ui.Text(c, a.tr("同步")).FontSize(fontSize).TextColor(color).SingleLine().Shrink(0)
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
