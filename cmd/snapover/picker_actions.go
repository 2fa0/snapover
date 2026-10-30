package main

import (
	"fmt"
	"runtime"

	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
)

func historyMatchTitle(c *ui.Context, entry history.Entry, selected bool) ui.Element {
	if entry.MatchField != "" {
		return ui.Text(c, entryTitle(entry)).SingleLine()
	}
	base := c.Theme().Text
	if selected {
		base = c.Theme().AccentText
	}
	return historyMatchSnippet(c, entry, base, selected)
}

func historyMatchDetail(c *ui.Context, entry history.Entry, color ui.Color, size float32, selected bool) {
	if entry.MatchField != "" && entry.MatchText != "" {
		historyMatchSnippet(c, entry, color, selected).FontSize(size)
	}
}

// historyMatchSnippet renders what matched: a snippet of the text, or of the
// field that carried the match, with every match inside it marked.
//
// The mark is a bold run on a soft block, which reads at 11 DIPs and keeps a
// cue beside its color. The chosen row fills with the accent color, where no
// block of the accent or its text shows, so there the mark turns the row's own
// contrast around instead: the matches stay at full strength while the text
// around them fades.
func historyMatchSnippet(c *ui.Context, entry history.Entry, base ui.Color, selected bool) ui.Element {
	if entry.MatchText == "" || len(entry.MatchRanges) == 0 {
		return ui.Text(c, entryTitle(entry)).SingleLine()
	}
	mark := matchMark{plain: base, match: base, block: c.Theme().Selection}
	if selected {
		mark = matchMark{plain: base.Alpha(0.78), match: base, block: base.Alpha(0.32)}
	}
	return ui.RichText(c, historyMatchSpans(entry, mark)...).MaxLines(2)
}

// matchMark is how a row draws the runs of a snippet it matched and those it
// did not.
type matchMark struct {
	plain ui.Color
	match ui.Color
	block ui.Color
}

// historyMatchSpans splits a snippet into runs of marked and unmarked text.
func historyMatchSpans(entry history.Entry, mark matchMark) []ui.Span {
	text := entry.MatchText
	spans := make([]ui.Span, 0, 2*len(entry.MatchRanges)+1)
	if entry.MatchField != "" {
		spans = append(spans, ui.Span{Text: entry.MatchField + "：", Color: mark.plain})
	}
	at := 0
	for _, r := range entry.MatchRanges {
		if r.Start < at || r.End <= r.Start || r.End > len(text) {
			continue
		}
		if r.Start > at {
			spans = append(spans, ui.Span{Text: text[at:r.Start], Color: mark.plain})
		}
		spans = append(spans, ui.Span{Text: text[r.Start:r.End], Color: mark.match, Weight: 700, Background: mark.block, Radius: 3})
		at = r.End
	}
	if at < len(text) {
		spans = append(spans, ui.Span{Text: text[at:], Color: mark.plain})
	}
	return spans
}

func pinShortcutLabel(key int) string {
	if runtime.GOOS == "darwin" {
		return fmt.Sprintf("⌘%d", key)
	}
	return fmt.Sprintf("Ctrl+%d", key)
}

// pickerActionKey is also attached to the input: Enter and deletion keys
// belong to its editor before window shortcuts. IME composition stays native.
func (a *app) pickerActionKey(mods ui.Modifiers, key ui.Key) bool {
	action := ""
	switch {
	case mods == ui.Alt && key == ui.KeyEnter:
		action = "copy"
	case mods == ui.Cmd|ui.Shift && key == ui.KeyP:
		action = "pin"
	case mods == ui.Cmd|ui.Shift && key == ui.KeyBackspace:
		action = "delete"
	case mods == ui.Cmd && key >= ui.Key1 && key <= ui.Key9:
		if a.pickBusy || a.historyCleanupPending != nil {
			return true
		}
		for _, entry := range a.entries {
			if entry.Pinned && entry.PinKey == int(key-ui.Key1)+1 {
				a.startHistoryPaste(entry.ID, a.picker)
				break
			}
		}
		return true
	default:
		return false
	}
	shown := a.filtered(a.pickQuery)
	if a.pickSel >= 0 && a.pickSel < len(shown) {
		a.performPickerAction(shown[a.pickSel].ID, action)
	}
	return true
}

func (a *app) performPickerAction(id, action string) {
	if a.pickBusy || a.historyCleanupPending != nil {
		a.pickFlash = "正在处理历史，请稍后再试"
		return
	}
	for _, entry := range a.entries {
		if entry.ID != id {
			continue
		}
		switch action {
		case "copy", "paste":
			a.startHistoryPasteMode(id, a.picker, action == "copy")
		case "pin", "delete":
			a.closePickPreview()
			a.pickRestore, a.pickRestoreID, a.pickRestoreIndex = true, id, a.pickSel
			a.pickMutationPending = true
			for i, shown := range a.filtered(a.pickQuery) {
				if shown.ID == id {
					a.pickRestoreIndex = i
					break
				}
			}
			a.pickFlash = "正在处理历史…"
			if action == "pin" {
				a.startHistoryMutation(func() error { return a.hist.SetPinned(id, !entry.Pinned) }, "已更新置顶", false)
			} else {
				a.startHistoryMutation(func() error { return a.hist.Delete(id) }, "已删除", false)
			}
		}
		return
	}
	a.pickFlash = "记录已删除，请重新选择"
}

func (a *app) pickerEntryMenu(m *ui.Menu, entry history.Entry) {
	busy := a.pickBusy || a.historyCleanupPending != nil
	files := entry.Kind == string(clip.KindFiles)
	if m.Item("粘贴").Shortcut(0, ui.KeyEnter).Disabled(busy || files).Chosen() {
		a.performPickerAction(entry.ID, "paste")
	}
	if m.Item("只复制").Shortcut(ui.Alt, ui.KeyEnter).Disabled(busy || files).Chosen() {
		a.performPickerAction(entry.ID, "copy")
	}
	if files {
		m.Item("文件正文未保存在历史中，请重新复制").Disabled(true)
	}
	m.Separator()
	label := "置顶"
	if entry.Pinned {
		label = "取消置顶"
	}
	if m.Item(label).Shortcut(ui.Cmd|ui.Shift, ui.KeyP).Disabled(busy).Chosen() {
		a.performPickerAction(entry.ID, "pin")
	}
	if entry.Pinned && entry.PinKey > 0 && entry.PinKey <= 9 {
		m.Item("快捷粘贴：" + pinShortcutLabel(entry.PinKey) + "（快速历史内）").Disabled(true)
	}
	if m.Item("删除").Shortcut(ui.Cmd|ui.Shift, ui.KeyBackspace).Disabled(busy).Chosen() {
		a.performPickerAction(entry.ID, "delete")
	}
}

func (a *app) restorePickSelection(shown []history.Entry) {
	if !a.pickRestore || a.historyCleanupPending != nil || a.pickerSearch.pending != nil {
		return
	}
	a.pickRestore = false
	a.pickSel = min(max(a.pickRestoreIndex, 0), len(shown)-1)
	for i, entry := range shown {
		if entry.ID == a.pickRestoreID {
			a.pickSel = i
			break
		}
	}
	if a.pickSel >= 0 {
		a.pickList.ScrollIntoView(a.pickSel)
	}
}
