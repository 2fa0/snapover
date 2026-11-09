package main

// Adapted from Lobe MyGo's Toast, Progress, Badge (status dot) and Skeleton
// (MIT, Copyright 2026 LobeHub). See licenses/lobe-mygo.txt for the sources and
// the license. The toasts keep MyGo's queue, timing, pointer hold, Escape and
// screen-reader behaviour and take Snapover's own look and status icons, so
// that an outcome that failed no longer reads like one that worked; the
// progress bar and the status dot take their colors from Snapover's theme.

import (
	"time"

	"github.com/egoist/mygo/ui"
)

var (
	toastSuccessIcon = interfaceIcon("lobe-circle-check")
	toastFailureIcon = interfaceIcon("lobe-circle-x")
	toastWarningIcon = interfaceIcon("lobe-triangle-alert")
	toastCloseIcon   = interfaceIcon("lobe-x")
)

// toastKind is what a toast reports, which picks its icon and its color.
type toastKind uint8

const (
	toastPlain toastKind = iota
	toastSuccess
	toastWarning
	toastFailure
)

// name is MyGo's name for the kind, which the toast look reads back.
func (k toastKind) name() string {
	switch k {
	case toastSuccess:
		return "success"
	case toastWarning:
		return "warning"
	case toastFailure:
		return "error"
	}
	return ""
}

// queuedToast is an outcome a worker hands to the next frame, which shows it:
// the toast queue itself is only built while the view runs.
type queuedToast struct {
	kind toastKind
	text string
}

func (a *app) notify(kind toastKind, message string) {
	if message == "" {
		return
	}
	a.pendingToast = queuedToast{kind: kind, text: message}
}

// showToast adds the toast queued since the last frame and draws the window's
// toasts in Snapover's look, in place of MyGo's plain bar.
func (a *app) showToast(c *ui.Context) {
	if queued := a.pendingToast; queued.text != "" {
		a.pendingToast = queuedToast{}
		c.AddToast(ui.Toast{ID: queued.text, Title: a.tr(queued.text), Type: queued.kind.name()})
	}
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		// Above the row of actions the history page keeps at its bottom edge,
		// which a toast over it would cover, and clear of the window's sides
		// in the narrow quick-history window.
		viewport.Bottom(64).Padding(0, 16).Gap(8).AlignItems(ui.Center).Justify(ui.End)
		for _, toast := range toasts {
			a.toastCard(c, toast)
		}
	})
}

// toastCard draws one toast: its outcome's icon, what it says, and a button
// that closes it before its time is over.
func (a *app) toastCard(c *ui.Context, toast ui.Toast) {
	t := c.Theme()
	parts := ui.ToastBase(c, toast)
	root := parts.Root.Row().AlignItems(ui.Start).Gap(10).Padding(10, 12).Radius(10).
		MaxWidth(420).Background(t.Surface).Border(1, t.Border).
		Shadow(0, 6, 20, -6, ui.RGBA(0, 0, 0, 0.3)).
		// Only the position animates: a transition that animates colors too
		// morphs the card's fill from an unset color, which shows as white.
		Transition(ui.ElementTransition{Duration: 150 * time.Millisecond, Ease: ui.EaseOut,
			Position: true, Enter: &ui.Motion{Y: 8}, Exit: &ui.Motion{Y: 8}})
	root.Children(func() {
		if icon, color := toastTone(t, toast.Type); icon != nil {
			ui.Row(c).Height(20).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Icon(c, icon).FontSize(16).TextColor(color)
			})
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, toast.Title).FontSize(13).FontWeight(500).LineHeight(1.4)
			if toast.Description != "" {
				ui.Text(c, toast.Description).FontSize(12).LineHeight(1.45).TextColor(t.TextMuted)
			}
		})
		close := parts.CloseButton().Label(a.tr("关闭提示")).Size(20, 20).Radius(4).Center().Shrink(0).
			Cursor(ui.CursorPointer).TextColor(t.TextMuted)
		if close.Hovered() {
			close.Background(t.SurfaceHover).TextColor(t.Text)
		}
		close.Children(func() { ui.Icon(c, toastCloseIcon).FontSize(12) })
	})
}

func toastTone(t *ui.Theme, kind string) (*ui.SVG, ui.Color) {
	switch kind {
	case "success":
		return toastSuccessIcon, t.Success
	case "warning":
		return toastWarningIcon, t.Warning
	case "error":
		return toastFailureIcon, t.Danger
	case "info":
		return infoIcon, t.Accent
	}
	return nil, t.Text
}

// progressTone is how the work a bar reports is going, which colors it and
// picks what shows beside it.
type progressTone uint8

const (
	toneRunning progressTone = iota
	toneDone
	toneFailed
)

const (
	// progressMove is how long the bar takes to reach a new share, so that a
	// burst of updates reads as one movement instead of jumps.
	progressMove = 300 * time.Millisecond
	// progressGlide is how long an unknown share takes to cross the track.
	progressGlide = 1400 * time.Millisecond
)

// progressBar draws how far along a transfer is, from 0 to 1: a negative
// value glides a segment across the track, for work whose size is not known
// yet. The tone colors the fill and ends it with a check or a cross, so that a
// finished transfer and a failed one do not look alike.
func progressBar(c *ui.Context, value float64, tone progressTone, label string) ui.Element {
	t := c.Theme()
	color := t.Accent
	switch tone {
	case toneDone:
		color = t.Success
	case toneFailed:
		color = t.Danger
	}
	share := float32(min(max(value, 0), 1))
	root := ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().
		Role(ui.RoleProgress).Range(0, 100, float64(share*100))
	root.Children(func() {
		track := ui.Box(c).Grow(1).MinWidth(0).Height(6).Radius(3).
			Background(t.Border).Clip().Label(label)
		track.Children(func() {
			if value < 0 {
				// The sender has not said how much is left: a segment crosses
				// the track rather than claim a share of it.
				at := track.Loop("glide", progressGlide, ui.EaseInOut)
				ui.Box(c).Absolute().Top(0).Bottom(0).WidthPercent(30).Radius(3).
					Background(color).TranslatePercent(333*at, 0)
				return
			}
			shown := root.AnimateTo("fill", share*100, ui.Animation{Duration: progressMove, Ease: ui.EaseOut})
			if shown > 0 {
				ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Radius(3).
					Background(color).WidthPercent(shown)
			}
		})
		if icon, iconColor := progressInfo(t, tone); icon != nil {
			ui.Row(c).MinWidth(16).Height(16).Center().Shrink(0).Children(func() {
				ui.Icon(c, icon).FontSize(14).TextColor(iconColor)
			})
		}
	})
	return root
}

func progressInfo(t *ui.Theme, tone progressTone) (*ui.SVG, ui.Color) {
	switch tone {
	case toneDone:
		return toastSuccessIcon, t.Success
	case toneFailed:
		return toastFailureIcon, t.Danger
	}
	return nil, t.Text
}

// statusLabel names a state beside a dot in its color, so that a list of
// devices can be read down one column; a pending state pulses, which tells a
// wait from a state that stays.
func statusLabel(c *ui.Context, text string, color ui.Color, pending bool) ui.Element {
	row := ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0)
	row.Children(func() {
		dot := ui.Box(c).Size(6, 6).Radius(3).Background(color).Shrink(0).IgnoreInput(true)
		if pending {
			pulse := dot.Loop("pulse", 1200*time.Millisecond, ui.EaseOut)
			dot.Children(func() {
				ui.Box(c).Absolute().Left(-1).Top(-1).Size(8, 8).Radius(4).Border(1, color).
					Scale(1+1.4*pulse, 1+1.4*pulse).Opacity(1 - pulse)
			})
		}
		ui.Text(c, text).FontSize(12).TextColor(color).SingleLine().
			ContentTransition(ui.ContentTransition{})
	})
	return row
}

// skeleton holds the place of something that is being read, so that what
// arrives does not move what is already there. Its opacity pulses, which says
// that it is waiting rather than empty.
func skeleton(c *ui.Context, width, height, radius float32) ui.Element {
	block := ui.Box(c).Height(height).Radius(radius).Background(c.Theme().Border.Alpha(0.55)).Shrink(0).Unselectable()
	if width > 0 {
		block.Width(width)
	} else {
		block.FillWidth()
	}
	block.Opacity(1 - 0.45*block.Loop("skeleton", 1600*time.Millisecond, ui.Bounce(ui.EaseInOut)))
	return block
}

// skeletonRows stands in for a list of rows that is still being counted: the
// lines narrow down the stack, as the rows they hold the place of will. It
// builds them in the element being built.
func skeletonRows(c *ui.Context, rows int) {
	widths := []float32{100, 82, 64, 74}
	for i := range rows {
		// As tall as the text of a row, so that the rows that arrive take
		// the room the lines hold rather than push the rest down.
		skeleton(c, 0, 15, 4).WidthPercent(widths[i%len(widths)])
	}
}
