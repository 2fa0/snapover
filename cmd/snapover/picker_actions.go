package main

import (
	"fmt"
	"runtime"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var revealFileInFolder = func(path string) { go mygo.Shell.ShowItemInFolder(path) }

func revealFileLabel() string {
	if runtime.GOOS == "darwin" {
		return "在 Finder 中显示"
	}
	return "在资源管理器中显示"
}

func (a *app) revealHistoryFile(entry history.Entry) {
	if len(entry.FilePaths) == 0 && len(entry.PinFiles) == 0 {
		return
	}
	if a.picker != nil {
		a.finishPick()
	}
	go func() {
		path, ok := history.RevealFilePath(entry)
		if !ok {
			a.update(func() { a.flash = "文件内容未保存在历史中，请重新复制后再粘贴" })
			return
		}
		revealFileInFolder(path)
	}()
}

func (a *app) historyMatchTitle(c *ui.Context, entry history.Entry, selected bool) ui.Element {
	if entry.MatchField != "" {
		return ui.Text(c, a.entryTitle(entry)).SingleLine()
	}
	base := c.Theme().Text
	if selected {
		base = c.Theme().AccentText
	}
	return a.historyMatchSnippet(c, entry, base, selected)
}

func (a *app) historyMatchDetail(c *ui.Context, entry history.Entry, color ui.Color, size float32, selected bool) {
	if entry.Kind == string(clip.KindFiles) && selected {
		message := a.fileAvailabilityMessage(c, entry)
		ui.Text(c, a.tr(message)).FontSize(size).TextColor(color).SingleLine().Tooltip(a.tr(message))
	}
	if entry.MatchField != "" && entry.MatchText != "" {
		a.historyMatchSnippet(c, entry, color, selected).FontSize(size)
		return
	}
	if entry.Pinned && entry.Note != "" {
		prefix := "备注："
		if a.isEN() {
			prefix = "Note: "
		}
		ui.Text(c, prefix+entry.Note).FontSize(size).TextColor(color).SingleLine()
	}
}

func (a *app) beginEditNote(entry history.Entry, fromPicker bool) {
	if !entry.Pinned {
		return
	}
	a.editNoteID = entry.ID
	a.editNoteText = entry.Note
	a.editNotePicker = fromPicker
}

func (a *app) cancelEditNote() {
	a.editNoteID = ""
	a.editNoteText = ""
	a.editNotePicker = false
}

func (a *app) saveEntryNote() {
	id := a.editNoteID
	note := a.editNoteText
	fromPicker := a.editNotePicker
	if id == "" {
		return
	}
	a.cancelEditNote()
	if fromPicker {
		a.pickRestore, a.pickRestoreID, a.pickRestoreIndex = true, id, a.pickSel
		a.pickMutationPending = true
	}
	msg := "已保存备注"
	if history.CleanNote(note) == "" {
		msg = "已清除备注"
	}
	a.startHistoryMutation(func() error { return a.hist.SetNote(id, note) }, msg, false)
}

func (a *app) clearEntryNote(id string, fromPicker bool) {
	if a.editNoteID == id {
		a.cancelEditNote()
	}
	if fromPicker {
		a.pickRestore, a.pickRestoreID, a.pickRestoreIndex = true, id, a.pickSel
		a.pickMutationPending = true
	}
	a.startHistoryMutation(func() error { return a.hist.SetNote(id, "") }, "已清除备注", false)
}

func (a *app) noteEditorRow(c *ui.Context, inPicker bool) {
	if a.editNoteID == "" || a.editNotePicker != inPicker {
		return
	}
	busy := a.historyCleanupPending != nil
	ui.Row(c.Key("note-editor")).Gap(8).AlignItems(ui.Center).Children(func() {
		input := ui.TextInput(c.Key("note-input"), &a.editNoteText).
			Label(a.tr("置顶备注")).
			Placeholder(a.tr("输入备注或别名，便于搜索")).
			AutoFocus().Grow(1).MinWidth(0)
		if input.Shortcut(0, ui.KeyEscape) {
			a.cancelEditNote()
			return
		}
		if (input.Submitted() || ui.PrimaryButton(c, a.tr("保存备注")).Disabled(busy).Clicked()) && !busy {
			a.saveEntryNote()
			return
		}
		if ui.Button(c, a.tr("取消")).Label(a.tr("取消编辑备注")).Clicked() {
			a.cancelEditNote()
		}
	})
}

// historyMatchSnippet renders what matched: a snippet of the text, or of the
// field that carried the match, with every match inside it marked.
//
// The mark is a bold run on a soft block, which reads at 11 DIPs and keeps a
// cue beside its color. The chosen row fills with the accent color, where no
// block of the accent or its text shows, so there the mark turns the row's own
// contrast around instead: the matches stay at full strength while the text
// around them fades.
func (a *app) historyMatchSnippet(c *ui.Context, entry history.Entry, base ui.Color, selected bool) ui.Element {
	if entry.MatchText == "" || len(entry.MatchRanges) == 0 {
		return ui.Text(c, a.entryTitle(entry)).SingleLine()
	}
	mark := matchMark{plain: base, match: base, block: c.Theme().Selection}
	if selected {
		mark = matchMark{plain: base.Alpha(0.78), match: base, block: base.Alpha(0.32)}
	}
	spans := historyMatchSpans(entry, mark)
	if a.isEN() && entry.MatchField != "" && len(spans) > 0 {
		spans[0].Text = a.tr(entry.MatchField) + ": "
	}
	return ui.RichText(c, spans...).MaxLines(2)
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
			if a.editNoteID == id {
				a.cancelEditNote()
			}
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
	available := !files || a.cachedFilesAvailable(entry)
	if m.Item(a.tr("粘贴")).Shortcut(0, ui.KeyEnter).Disabled(busy || !available).Chosen() {
		a.performPickerAction(entry.ID, "paste")
	}
	if m.Item(a.tr("只复制")).Shortcut(ui.Alt, ui.KeyEnter).Disabled(busy || !available).Chosen() {
		a.performPickerAction(entry.ID, "copy")
	}
	if files && available {
		if m.Item(a.tr(revealFileLabel())).Disabled(busy).Chosen() {
			a.revealHistoryFile(entry)
		}
	}
	if files && !available {
		m.Item(a.tr("文件正文未保存在历史中，请重新复制")).Disabled(true)
	}
	if entry.Kind == string(clip.KindImage) {
		if m.Item(a.tr("放大查看")).Chosen() {
			// The quick history steps aside for the picture, as it does for
			// the system viewer.
			a.closePicker()
			a.openImageViewer(entry)
		}
	}
	m.Separator()
	label := a.tr("置顶")
	if entry.Pinned {
		label = a.tr("取消置顶")
	}
	if m.Item(label).Shortcut(ui.Cmd|ui.Shift, ui.KeyP).Disabled(busy).Chosen() {
		a.performPickerAction(entry.ID, "pin")
	}
	if entry.Pinned {
		noteItem := "设置备注…"
		if entry.Note != "" {
			noteItem = "修改备注…"
		}
		if m.Item(a.tr(noteItem)).Disabled(busy).Chosen() {
			a.beginEditNote(entry, true)
		}
		if entry.Note != "" && m.Item(a.tr("清除备注")).Disabled(busy).Chosen() {
			a.clearEntryNote(entry.ID, true)
		}
	}
	if entry.Pinned && entry.PinKey > 0 && entry.PinKey <= 9 {
		m.Item(a.tr("快捷粘贴：" + pinShortcutLabel(entry.PinKey) + "（快速历史内）")).Disabled(true)
	}
	if m.Item(a.tr("删除")).Shortcut(ui.Cmd|ui.Shift, ui.KeyBackspace).Disabled(busy).Chosen() {
		a.performPickerAction(entry.ID, "delete")
	}
}

func (a *app) historyEntryMenu(m *ui.Menu, entry history.Entry) {
	busy := a.historyPaste != nil || a.pasteWorker != nil || a.historyCleanupPending != nil
	files := entry.Kind == string(clip.KindFiles)
	available := !files || a.cachedFilesAvailable(entry)
	if m.Item(a.tr("复制")).Shortcut(0, ui.KeyEnter).Disabled(busy || !available).Chosen() {
		a.startHistoryPaste(entry.ID, nil)
	}
	if files && available {
		if m.Item(a.tr(revealFileLabel())).Disabled(busy).Chosen() {
			a.revealHistoryFile(entry)
		}
	}
	if files && !available {
		m.Item(a.tr("文件正文未保存在历史中，请重新复制")).Disabled(true)
	}
	if entry.Kind == string(clip.KindImage) {
		if m.Item(a.tr("放大查看")).Chosen() {
			a.openImageViewer(entry)
		}
	}
	m.Separator()
	label := a.tr("置顶")
	if entry.Pinned {
		label = a.tr("取消置顶")
	}
	if m.Item(label).Disabled(a.historyCleanupPending != nil).Chosen() {
		a.startHistoryMutation(func() error { return a.hist.SetPinned(entry.ID, !entry.Pinned) }, "已更新置顶", false)
	}
	if entry.Pinned {
		noteItem := "设置备注…"
		if entry.Note != "" {
			noteItem = "修改备注…"
		}
		if m.Item(a.tr(noteItem)).Disabled(a.historyCleanupPending != nil).Chosen() {
			a.beginEditNote(entry, false)
		}
		if entry.Note != "" && m.Item(a.tr("清除备注")).Disabled(a.historyCleanupPending != nil).Chosen() {
			a.clearEntryNote(entry.ID, false)
		}
	}
	if entry.Pinned && entry.PinKey > 0 && entry.PinKey <= 9 {
		m.Item(a.tr("快捷粘贴：" + pinShortcutLabel(entry.PinKey) + "（快速历史内）")).Disabled(true)
	}
	if m.Item(a.tr("删除")).Disabled(a.historyCleanupPending != nil).Chosen() {
		a.startHistoryMutation(func() error { return a.hist.Delete(entry.ID) }, "已删除", false)
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
