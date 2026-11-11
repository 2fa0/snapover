package main

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	"github.com/egoist/mygo/ui"
)

func configureLanguage(lang string) {
	mygo.App.SetLanguages(settings.LangEN, settings.LangZH)
	mygo.App.SetLocale(lang)
}

func (a *app) localeChanged(locale string) {
	next := languageFromTags([]string{locale})
	if a.systemLang != next {
		a.systemLang = next
		if a.cfg == nil || a.cfg.Values.Language == settings.LangAuto {
			a.refreshLanguageUI()
		}
	}
}

func languageFromTags(tags []string) string {
	for _, tag := range tags {
		switch i18n.Base(tag) {
		case "zh":
			return settings.LangZH
		case "en":
			return settings.LangEN
		}
	}
	for _, tag := range tags {
		if i18n.Base(tag) != "" {
			return settings.LangEN
		}
	}
	return settings.LangZH
}

func (a *app) lang() string {
	if a != nil && a.cfg != nil {
		switch a.cfg.Values.Language {
		case settings.LangZH, settings.LangEN:
			return a.cfg.Values.Language
		}
	}
	if a != nil && a.systemLang == settings.LangEN {
		return settings.LangEN
	}
	return settings.LangZH
}

func (a *app) isEN() bool {
	return a.lang() == settings.LangEN
}

func (a *app) tr(s string) string {
	if !a.isEN() || s == "" {
		return s
	}
	return translateEN(s)
}

func (a *app) trf(zhFormat, enFormat string, args ...any) string {
	if a.isEN() {
		return fmt.Sprintf(enFormat, args...)
	}
	return fmt.Sprintf(zhFormat, args...)
}

// Titles can contain clipboard text and file names; only their metadata is translated.
func (a *app) entryTitle(entry history.Entry) string {
	switch entry.Kind {
	case string(clip.KindImage):
		return a.tr(entryTitle(entry))
	case string(clip.KindFiles):
		if entry.FileCount > 1 {
			return a.trf("%s 等 %d 项", "%s (%d items)", entry.FileName, entry.FileCount)
		}
		if entry.FileName == "" {
			return a.tr("文件")
		}
	default:
		if strings.TrimSpace(entry.Text) == "" {
			return a.tr("空文本")
		}
	}
	return entryTitle(entry)
}

func (a *app) displayName(name, id string) string {
	if name == "" && id == "" {
		return a.tr("未知设备")
	}
	return displayName(name, id)
}

func (a *app) languageSettings(c *ui.Context) {
	selected := 0
	if a.cfg != nil {
		switch a.cfg.Values.Language {
		case settings.LangZH:
			selected = 1
		case settings.LangEN:
			selected = 2
		}
	}
	settingRow(c, a.tr("界面语言"), "", func() {
		if ui.Segmented(c, &selected, a.tr("跟随系统"), "简体中文", "English").Label(a.tr("界面语言")).Changed() {
			a.saveLanguageChoice(selected)
		}
	})
	if a.languageError != "" {
		ui.Text(c, a.tr(a.languageError)).FontSize(12).TextColor(c.Theme().Danger)
	}
}

func (a *app) saveLanguageChoice(selected int) {
	if a.cfg == nil {
		return
	}
	want := settings.LangAuto
	switch selected {
	case 1:
		want = settings.LangZH
	case 2:
		want = settings.LangEN
	}
	prev := a.cfg.Values.Language
	if prev == want {
		a.languageError = ""
		return
	}
	a.cfg.Values.Language = want
	if err := a.cfg.Save(); err != nil {
		a.cfg.Values.Language = prev
		a.languageError = "保存设置失败：" + err.Error()
		return
	}
	a.languageError = ""
	if a.root != nil {
		mygo.App.SetLocale(want)
	}
	a.refreshLanguageUI()
}

func (a *app) refreshLanguageUI() {
	if a.root != nil && runtime.GOOS == "darwin" {
		mygo.App.SetMenu(a.windowAppMenu())
	}
	a.refreshTray()
	if a.fileWin != nil {
		a.fileWin.SetTitle(a.tr("发送文件 · Snapover"))
		a.fileWin.Invalidate()
	}
	if a.transferWin != nil {
		a.transferWin.SetTitle(a.tr("文件同步 · Snapover"))
		a.transferWin.Invalidate()
	}
	if a.networkGuide != nil && a.networkGuide.win != nil {
		a.networkGuide.win.SetTitle(a.tr("连接指引 · Snapover"))
		a.networkGuide.win.Invalidate()
	}
	if a.pickOriginal != nil {
		a.pickOriginal.SetTitle(a.tr("文本全文"))
		a.pickOriginal.Invalidate()
	}
	if v := a.imageViewer; v != nil && v.win != nil {
		v.win.SetTitle(a.tr("图片预览"))
		v.win.Invalidate()
	}
	if a.picker != nil {
		a.picker.Invalidate()
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

func translateEN(s string) string {
	if s == "" {
		return ""
	}
	if en, ok := enStrings[s]; ok {
		return en
	}
	if en, ok := translateENPattern(s); ok {
		return en
	}
	if idx := strings.Index(s, "："); idx > 0 && idx+len("：") < len(s) {
		head := s[:idx]
		tail := s[idx+len("："):]
		return translateEN(head) + ": " + translateEN(tail)
	}
	return s
}

func translateENPattern(s string) (string, bool) {
	if strings.Contains(s, "\n") {
		lines := strings.Split(s, "\n")
		changed := false
		for i, line := range lines {
			if line == "" {
				continue
			}
			trLine := translateEN(line)
			if trLine != line {
				changed = true
			}
			lines[i] = trLine
		}
		if changed {
			return strings.Join(lines, "\n"), true
		}
	}

	// Relative time
	if strings.HasSuffix(s, " 秒前") {
		return strings.TrimSuffix(s, " 秒前") + "s ago", true
	}
	if strings.HasSuffix(s, " 分钟前") {
		return strings.TrimSuffix(s, " 分钟前") + "m ago", true
	}
	if strings.HasSuffix(s, " 小时前") {
		return strings.TrimSuffix(s, " 小时前") + "h ago", true
	}

	// Connection & route statuses
	if strings.HasPrefix(s, "已连接 · ") {
		route := strings.TrimPrefix(s, "已连接 · ")
		return "Connected · " + translateEN(route), true
	}
	if strings.HasPrefix(s, "已通过") && strings.Contains(s, "连接 ") {
		rest := strings.TrimPrefix(s, "已通过")
		parts := strings.SplitN(rest, "连接 ", 2)
		if len(parts) == 2 {
			return "Connected to " + parts[1] + " via " + translateEN(parts[0]), true
		}
	}
	if strings.HasPrefix(s, "正在通过") && strings.Contains(s, "连接 ") {
		rest := strings.TrimPrefix(s, "正在通过")
		parts := strings.SplitN(rest, "连接 ", 2)
		if len(parts) == 2 {
			return "Connecting to " + parts[1] + " via " + translateEN(parts[0]), true
		}
	}
	if strings.HasPrefix(s, "正在测试") && strings.Contains(s, "地址 ") {
		rest := strings.TrimPrefix(s, "正在测试")
		parts := strings.SplitN(rest, "地址 ", 2)
		if len(parts) == 2 {
			return "Testing " + translateEN(parts[0]) + " address " + parts[1], true
		}
	}
	if strings.HasPrefix(s, "已通过") && strings.Contains(s, "地址 ") && strings.Contains(s, " 拨出。") {
		rest := strings.TrimPrefix(s, "已通过")
		parts := strings.SplitN(rest, " 拨出。", 2)
		if len(parts) == 2 {
			addrParts := strings.SplitN(parts[0], "地址 ", 2)
			if len(addrParts) == 2 {
				return "Dialed from " + translateEN(addrParts[0]) + " address " + addrParts[1] + ". " + translateEN(parts[1]), true
			}
		}
	}
	if strings.Contains(s, "连接测试失败：") && strings.Contains(s, "。请确认对端运行，并检查到 TCP ") {
		idx1 := strings.Index(s, "连接测试失败：")
		idx2 := strings.LastIndex(s, "。请确认对端运行，并检查到 TCP ")
		if idx1 >= 0 && idx2 > idx1 {
			route := translateEN(s[:idx1])
			errMsg := s[idx1+len("连接测试失败：") : idx2]
			port := strings.TrimSuffix(s[idx2+len("。请确认对端运行，并检查到 TCP "):], " 的访问权限")
			return route + " connection test failed: " + errMsg + ". Ensure the peer is running and TCP port " + port + " is reachable", true
		}
	}
	if strings.HasPrefix(s, "已连接 ") {
		return "Connected to " + strings.TrimPrefix(s, "已连接 "), true
	}
	if strings.HasPrefix(s, "正在连接 ") {
		return "Connecting to " + strings.TrimPrefix(s, "正在连接 "), true
	}
	if strings.HasPrefix(s, "已断开与 ") && strings.HasSuffix(s, " 的连接") {
		peer := strings.TrimSuffix(strings.TrimPrefix(s, "已断开与 "), " 的连接")
		return "Disconnected from " + peer, true
	}

	// Pairing & verification notices
	if strings.HasPrefix(s, "等待配对 · ") && strings.HasSuffix(s, " 秒") {
		sec := strings.TrimSuffix(strings.TrimPrefix(s, "等待配对 · "), " 秒")
		return "Waiting to pair · " + sec + "s", true
	}
	if strings.HasPrefix(s, "剩余 ") && strings.HasSuffix(s, " 秒") {
		sec := strings.TrimSuffix(strings.TrimPrefix(s, "剩余 "), " 秒")
		return sec + "s remaining", true
	}
	if strings.HasPrefix(s, "已添加并连接 ") {
		return "Paired and connected to " + strings.TrimPrefix(s, "已添加并连接 "), true
	}
	if strings.HasPrefix(s, "已忽略 ") && strings.HasSuffix(s, " 的配对请求，请先点击「添加设备」") {
		peer := strings.TrimSuffix(strings.TrimPrefix(s, "已忽略 "), " 的配对请求，请先点击「添加设备」")
		return "Ignored pairing request from " + peer + "; click \"Add Device\" first", true
	}
	if strings.HasPrefix(s, "设备证书已变更：") {
		peer := strings.TrimPrefix(s, "设备证书已变更：")
		return "Device certificate changed: " + peer, true
	}
	if strings.HasPrefix(s, "拒绝连接 ") && strings.Contains(s, "：设备证书已变更") {
		name := strings.SplitN(strings.TrimPrefix(s, "拒绝连接 "), "：", 2)[0]
		return "Connection to " + name + " refused: device certificate changed. Unpair and pair again if the peer was reinstalled", true
	}

	// Clipboard sync notices (from internal/sync)
	if strings.HasPrefix(s, "已收到来自 ") && strings.HasSuffix(s, " 的文字") {
		peer := strings.TrimSuffix(strings.TrimPrefix(s, "已收到来自 "), " 的文字")
		return "Received text from " + peer, true
	}
	if strings.HasPrefix(s, "已收到来自 ") && strings.HasSuffix(s, " 的图片") {
		peer := strings.TrimSuffix(strings.TrimPrefix(s, "已收到来自 "), " 的图片")
		return "Received image from " + peer, true
	}
	if strings.HasPrefix(s, "已收到来自 ") && strings.HasSuffix(s, " 的文件") {
		peer := strings.TrimSuffix(strings.TrimPrefix(s, "已收到来自 "), " 的文件")
		return "Received file from " + peer, true
	}
	if strings.HasPrefix(s, "已发送文字到 ") {
		return "Sent text to " + strings.TrimPrefix(s, "已发送文字到 "), true
	}
	if strings.HasPrefix(s, "已发送图片到 ") {
		return "Sent image to " + strings.TrimPrefix(s, "已发送图片到 "), true
	}
	if strings.HasPrefix(s, "已发送文件到 ") {
		return "Sent file to " + strings.TrimPrefix(s, "已发送文件到 "), true
	}
	if strings.HasPrefix(s, "正在发送到 ") {
		return "Sending to " + strings.TrimPrefix(s, "正在发送到 "), true
	}
	if strings.HasPrefix(s, "正在打包 ") && strings.HasSuffix(s, "…") {
		return "Packing " + strings.TrimSuffix(strings.TrimPrefix(s, "正在打包 "), "…") + "…", true
	}
	if strings.HasPrefix(s, "正在同步 ") && strings.Contains(s, " · ") {
		return "Syncing " + strings.TrimPrefix(s, "正在同步 "), true
	}
	if strings.HasPrefix(s, "正在同步 ") && strings.HasSuffix(s, "…") {
		return "Syncing " + strings.TrimPrefix(s, "正在同步 "), true
	}
	if strings.HasPrefix(s, "正在整理 ") && strings.HasSuffix(s, "…") {
		return "Finishing " + strings.TrimSuffix(strings.TrimPrefix(s, "正在整理 "), "…") + "…", true
	}
	if strings.HasPrefix(s, "对方已可粘贴 ") {
		return "Peer can paste " + strings.TrimPrefix(s, "对方已可粘贴 "), true
	}
	if strings.HasPrefix(s, "已可粘贴 ") {
		return "Ready to paste " + strings.TrimPrefix(s, "已可粘贴 "), true
	}
	if strings.HasPrefix(s, "已发送 ") && !strings.Contains(s, " 个文件") {
		return "Sent " + strings.TrimPrefix(s, "已发送 "), true
	}

	// Limits & settings status messages
	if strings.HasPrefix(s, "文本大小上限须为 1–") && strings.HasSuffix(s, " 的整数，单位 MB") {
		maxMB := strings.TrimSuffix(strings.TrimPrefix(s, "文本大小上限须为 1–"), " 的整数，单位 MB")
		return "Max text size must be an integer between 1 and " + maxMB + " MB", true
	}
	if strings.HasPrefix(s, "文本大小上限已设为 ") && strings.HasSuffix(s, " MB") {
		mb := strings.TrimSuffix(strings.TrimPrefix(s, "文本大小上限已设为 "), " MB")
		return "Max text size set to " + mb + " MB", true
	}
	if strings.HasPrefix(s, "文件复制同步上限已设为 ") && strings.HasSuffix(s, " MB，下一次复制同步生效") {
		mb := strings.TrimSuffix(strings.TrimPrefix(s, "文件复制同步上限已设为 "), " MB，下一次复制同步生效")
		return "File clipboard limit set to " + mb + " MB (takes effect on next clipboard sync)", true
	}
	if strings.HasPrefix(s, "文本超过 ") && strings.HasSuffix(s, " MB 上限") {
		mb := strings.TrimSuffix(strings.TrimPrefix(s, "文本超过 "), " MB 上限")
		return "Text exceeds " + mb + " MB limit", true
	}
	if strings.HasPrefix(s, "图片超过 ") && strings.HasSuffix(s, " MB 上限") {
		mb := strings.TrimSuffix(strings.TrimPrefix(s, "图片超过 "), " MB 上限")
		return "Image exceeds " + mb + " MB limit", true
	}
	if strings.HasPrefix(s, "复制内容合计超过 ") && strings.HasSuffix(s, " MB") {
		mb := strings.TrimSuffix(strings.TrimPrefix(s, "复制内容合计超过 "), " MB")
		return "Copied files exceed " + mb + " MB in total", true
	}
	if strings.HasPrefix(s, "文件大小超过 ") && strings.HasSuffix(s, " MB 上限") {
		mb := strings.TrimSuffix(strings.TrimPrefix(s, "文件大小超过 "), " MB 上限")
		return "File size exceeds " + mb + " MB limit", true
	}

	// File transfer window & notifications
	if strings.HasPrefix(s, "发送到 ") {
		return "Send to " + strings.TrimPrefix(s, "发送到 "), true
	}
	if strings.HasPrefix(s, "可多选文件，每个最大 ") && strings.HasSuffix(s, " MB") {
		mb := strings.TrimSuffix(strings.TrimPrefix(s, "可多选文件，每个最大 "), " MB")
		return "Multiple files supported, up to " + mb + " MB each", true
	}
	if strings.Contains(s, " 个文件：") {
		parts := strings.SplitN(s, " 个文件：", 2)
		if len(parts) == 2 {
			rest := strings.ReplaceAll(parts[1], "、", ", ")
			if strings.HasSuffix(rest, " 等") {
				rest = strings.TrimSuffix(rest, " 等") + ", etc."
			}
			return parts[0] + " files: " + rest, true
		}
	}
	if strings.HasPrefix(s, "正在发送 ") && strings.Contains(s, " · ") {
		rest := strings.TrimPrefix(s, "正在发送 ")
		if idx := strings.Index(rest, "："); idx > 0 {
			return "Sending " + rest[:idx] + ": " + rest[idx+len("："):], true
		}
		return "Sending " + rest, true
	}
	if strings.HasPrefix(s, "发送失败：") && strings.HasSuffix(s, "。请检查连接后重试") {
		errMsg := strings.TrimSuffix(strings.TrimPrefix(s, "发送失败："), "。请检查连接后重试")
		return "Send failed: " + translateEN(errMsg) + ". Check the connection and try again", true
	}
	if strings.HasPrefix(s, "已发送 ") && strings.Contains(s, " 个文件后失败：") && strings.HasSuffix(s, "。请检查连接后重试") {
		rest := strings.TrimSuffix(strings.TrimPrefix(s, "已发送 "), "。请检查连接后重试")
		parts := strings.SplitN(rest, " 个文件后失败：", 2)
		if len(parts) == 2 {
			return "Failed after sending " + parts[0] + " files: " + translateEN(parts[1]) + ". Check the connection and try again", true
		}
	}
	if strings.HasPrefix(s, "已发送 ") && strings.Contains(s, " 个文件。") {
		parts := strings.SplitN(strings.TrimPrefix(s, "已发送 "), " 个文件。", 2)
		return "Sent " + parts[0] + " files. " + translateEN(parts[1]), true
	}
	if strings.HasPrefix(s, "已发送 ") && strings.HasSuffix(s, " 个文件") {
		count := strings.TrimSuffix(strings.TrimPrefix(s, "已发送 "), " 个文件")
		return "Sent " + count + " files", true
	}
	if strings.HasSuffix(s, " 的接收文件夹") && strings.Contains(s, " 个文件已保存到 ") {
		peer := strings.TrimSuffix(s, " 的接收文件夹")
		parts := strings.SplitN(peer, " 个文件已保存到 ", 2)
		if len(parts) == 2 {
			return parts[0] + " files saved to " + parts[1] + "'s receive folder", true
		}
	}
	if strings.HasSuffix(s, " 的接收文件夹") && strings.Contains(s, " 已保存到 ") {
		peer := strings.TrimSuffix(s, " 的接收文件夹")
		parts := strings.SplitN(peer, " 已保存到 ", 2)
		if len(parts) == 2 {
			return parts[0] + " saved to " + parts[1] + "'s receive folder", true
		}
	}
	if strings.HasPrefix(s, "收到 ") && strings.HasSuffix(s, "，已保存到接收文件夹") {
		name := strings.TrimSuffix(strings.TrimPrefix(s, "收到 "), "，已保存到接收文件夹")
		return "Received " + name + " (saved to receive folder)", true
	}
	if strings.HasSuffix(s, " 已保存到接收文件夹") {
		name := strings.TrimSuffix(s, " 已保存到接收文件夹")
		return name + " saved to receive folder", true
	}
	if strings.HasPrefix(s, "正在接收 ") {
		return "Receiving " + strings.TrimPrefix(s, "正在接收 "), true
	}
	if strings.HasPrefix(s, "打开接收位置失败：") && strings.HasSuffix(s, "。请从文件管理器打开接收文件夹") {
		errMsg := strings.TrimSuffix(strings.TrimPrefix(s, "打开接收位置失败："), "。请从文件管理器打开接收文件夹")
		return "Failed to open receive folder: " + errMsg + ". Open it from your file manager instead", true
	}

	// SendTo status
	if strings.HasPrefix(s, "更新 ") && strings.Contains(s, " 失败：") && strings.HasSuffix(s, "。请在设置中重新启用") {
		rest := strings.TrimSuffix(strings.TrimPrefix(s, "更新 "), "。请在设置中重新启用")
		parts := strings.SplitN(rest, " 失败：", 2)
		if len(parts) == 2 {
			return "Failed to update " + translateEN(parts[0]) + ": " + parts[1] + ". Re-enable it in Settings", true
		}
	}
	if strings.HasPrefix(s, "保存 ") && strings.HasSuffix(s, " 设置失败，请重试") {
		entry := strings.TrimSuffix(strings.TrimPrefix(s, "保存 "), " 设置失败，请重试")
		return "Failed to save " + translateEN(entry) + " setting, please try again", true
	}
	if strings.HasPrefix(s, "已启用 ") {
		return "Enabled " + translateEN(strings.TrimPrefix(s, "已启用 ")), true
	}
	if strings.HasPrefix(s, "已停用 ") {
		return "Disabled " + translateEN(strings.TrimPrefix(s, "已停用 ")), true
	}

	// History & search & entries
	if strings.HasPrefix(s, "图片 ") {
		return "Image " + strings.TrimPrefix(s, "图片 "), true
	}
	if strings.HasPrefix(s, "文件夹 · ") {
		rest := strings.TrimPrefix(s, "文件夹 · ")
		if idx := strings.LastIndex(rest, "（"); idx > 0 && strings.HasSuffix(rest, " 项）") {
			name := rest[:idx]
			count := strings.TrimSuffix(rest[idx+len("（"):], " 项）")
			return "Folder · " + name + " (" + count + " items)", true
		}
		return "Folder · " + rest, true
	}
	if strings.HasPrefix(s, "文件 · ") {
		rest := strings.TrimPrefix(s, "文件 · ")
		if strings.Contains(rest, " 等 ") && strings.HasSuffix(rest, " 项") {
			idx := strings.LastIndex(rest, " 等 ")
			name := rest[:idx]
			count := strings.TrimSuffix(rest[idx+len(" 等 "):], " 项")
			return "Files · " + name + " +" + count + " items", true
		}
		return "File · " + rest, true
	}
	if strings.HasSuffix(s, " 条记录") {
		return strings.TrimSuffix(s, " 条记录") + " items", true
	}
	if strings.HasSuffix(s, " 条") && len(s) < 24 {
		num := strings.TrimSuffix(s, " 条")
		if num != "" && num[0] >= '0' && num[0] <= '9' {
			return num + " items", true
		}
	}
	if strings.HasSuffix(s, " · 还有更多内容") {
		return strings.TrimSuffix(s, " · 还有更多内容") + " · more content", true
	}
	if strings.HasPrefix(s, "第 ") && strings.HasSuffix(s, " 段") && strings.Contains(s, " / ") {
		mid := strings.TrimSuffix(strings.TrimPrefix(s, "第 "), " 段")
		return "Part " + mid, true
	}
	if strings.HasPrefix(s, "快捷粘贴：") && strings.HasSuffix(s, "（快速历史内）") {
		key := strings.TrimSuffix(strings.TrimPrefix(s, "快捷粘贴："), "（快速历史内）")
		return "Quick Paste: " + key + " (in Quick History)", true
	}
	if strings.HasPrefix(s, "已将损坏历史移到 ") {
		return "Moved corrupted history to " + strings.TrimPrefix(s, "已将损坏历史移到 "), true
	}
	if strings.HasPrefix(s, "损坏历史备份（") && strings.HasSuffix(s, " 份）") {
		count := strings.TrimSuffix(strings.TrimPrefix(s, "损坏历史备份（"), " 份）")
		return "Corrupted History Backups (" + count + ")", true
	}
	if strings.HasSuffix(s, "无法统计，请检查文件夹访问权限后刷新。") {
		label := strings.TrimSuffix(s, "无法统计，请检查文件夹访问权限后刷新。")
		return "Failed to calculate " + translateEN(label) + "; check folder permissions and refresh.", true
	}

	// Shortcuts
	if strings.HasPrefix(s, "无法注册 ") && strings.HasSuffix(s, "，可能被占用，请换一组快捷键后重试") {
		key := strings.TrimSuffix(strings.TrimPrefix(s, "无法注册 "), "，可能被占用，请换一组快捷键后重试")
		return "Failed to register " + key + " (may be in use by another app). Try another shortcut", true
	}
	if strings.HasPrefix(s, "快捷键无法注册：") && strings.HasSuffix(s, "。请在设置中更换，也可从托盘打开快速历史") {
		key := strings.TrimSuffix(strings.TrimPrefix(s, "快捷键无法注册："), "。请在设置中更换，也可从托盘打开快速历史")
		return "Failed to register shortcut: " + key + ". Change it in Settings or open Quick History from the tray", true
	}
	if strings.HasSuffix(s, "。原快捷键保持不变") {
		head := strings.TrimSuffix(s, "。原快捷键保持不变")
		return translateEN(head) + ". Previous shortcut kept", true
	}

	// Segment labels like "192.168.1.0/24 · en0 · 本机 192.168.1.5"
	if strings.Contains(s, " · 本机 ") {
		out := strings.Replace(s, " · 本机 ", " · Local ", 1)
		out = strings.ReplaceAll(out, "、", ", ")
		out = strings.Replace(out, " · 仅用于 Tailscale 地址连接（需开启），不参与发现", " · Tailscale address only (when enabled), not used for discovery", 1)
		out = strings.Replace(out, " · 不用于连接", " · Not used for connections", 1)
		return out, true
	}
	if strings.Contains(s, "  ·  仅用于 Tailscale 地址连接（需开启），不参与发现") {
		return strings.Replace(s, "  ·  仅用于 Tailscale 地址连接（需开启），不参与发现", "  ·  Tailscale address only (when enabled), not used for discovery", 1), true
	}
	if strings.Contains(s, "  ·  不用于连接") {
		return strings.Replace(s, "  ·  不用于连接", "  ·  Not used for connections", 1), true
	}

	// Updates & Network Guide
	if strings.HasPrefix(s, "当前版本 ") {
		ver := strings.TrimPrefix(s, "当前版本 ")
		return "Current Version " + translateEN(ver), true
	}
	if strings.HasPrefix(s, "上次检查：") {
		return "Last checked: " + strings.TrimPrefix(s, "上次检查："), true
	}
	if strings.HasPrefix(s, "已发现 ") && strings.HasSuffix(s, " 台电脑") {
		count := strings.TrimSuffix(strings.TrimPrefix(s, "已发现 "), " 台电脑")
		return "Found " + count + " computer(s)", true
	}
	if strings.HasPrefix(s, "本机连接端口为 TCP ") && strings.HasSuffix(s, "，自动发现使用 UDP 5353。") {
		port := strings.TrimSuffix(strings.TrimPrefix(s, "本机连接端口为 TCP "), "，自动发现使用 UDP 5353。")
		return "Local connection port is TCP " + port + "; auto-discovery uses UDP 5353.", true
	}
	if strings.HasPrefix(s, "当前程序：") {
		return "Current executable: " + strings.TrimPrefix(s, "当前程序："), true
	}

	return "", false
}

var enStrings = map[string]string{
	"隐私模式": "Privacy Mode",
	"隐私模式：不记录、不收发剪贴板":                  "Privacy Mode: history and clipboard sync are off",
	"不记录本机复制，不收发剪贴板；重启后保持。主动发送文件仍可使用。": "Stops local history and clipboard sending/receiving; persists after restart. Direct file sending remains available.",
	"排除来源应用":  "Exclude Source Apps",
	"排除的应用名称": "Excluded App Names",
	"保存应用排除":  "Save App Exclusions",
	"填写历史中显示的来源应用名，用分号分隔；这些应用的本机复制不记录、不同步。无法识别来源时仍按普通复制处理。": "Enter source app names as shown in history, separated by semicolons. Copies from these apps are neither recorded nor synced. Unknown sources are handled normally.",
	"隐私设置保存失败，请检查数据目录权限后重试":                                 "Could not save privacy settings; check data directory permissions and try again",
	"隐私设置已阻止记录和同步": "Privacy settings blocked history and sync",
	"%d 项":         "%d items",
	"按最新到最旧的顺序导出；范围留空表示全部。大备份可填写起止序号分批导出，每批最多 1 GiB。": "Export newest first. Leave the range blank for all pins, or enter positions to export batches of up to 1 GiB.",
	"起始序号":       "First item",
	"结束序号":       "Last item",
	"请填写有效的起止序号": "Enter a valid first and last item number",
	"置顶备份":       "Pinned Backup",
	"导出置顶":       "Export Pins",
	"导入置顶":       "Import Pins",
	"备份包含置顶文本、图片和备注，不含文件正文；备份未加密，请妥善保管。导入会追加记录，容量不足时按历史规则清理未置顶内容。": "Backups include pinned text, images and notes, without file contents. Backups are unencrypted; keep them safe. Import appends records and may remove unpinned history under the storage limit.",
	"正在处理置顶备份…":           "Processing pinned backup…",
	"置顶备份已完成：%d 条":        "Pinned backup complete: %d items",
	"置顶备份未完成，已处理 %d 条：%s": "Pinned backup incomplete; processed %d items: %s",
	"生成诊断摘要":              "Generate Diagnostic Summary",
	"复制诊断摘要":              "Copy Diagnostic Summary",
	"复制诊断摘要失败，请重试":        "Could not copy the diagnostic summary; try again",
	"已复制诊断摘要":             "Diagnostic summary copied",
	"摘要不含剪贴板正文、文件路径、设备名、地址或证书。TCP 测试通过不代表剪贴板写入成功。": "The summary excludes clipboard contents, file paths, device names, addresses and certificates. A TCP test does not confirm a clipboard write.",
	"最近同步结果":           "Last Sync Result",
	"已连接（TLS 配对会话）":    "Connected (paired TLS session)",
	"暂无结果":             "No result yet",
	"只存入对端历史，未覆盖剪贴板":   "Saved to peer history without replacing the clipboard",
	"失败或未确认，请检查对端后重试":  "Failed or unconfirmed; check the peer before retrying",
	"被同步策略阻止或对端拒收":     "Blocked by sync policy or rejected by peer",
	"对端已确认写入剪贴板":       "Peer confirmed clipboard write",
	"尚无可归类的确认结果，请测试连接": "No classified confirmation yet; test the connection",
	// App & Menu & Sidebar
	"开发版":        "Dev",
	"设置":         "Settings",
	"设置…":        "Settings…",
	"连接设备":       "Devices",
	"复制历史":       "History",
	"未连接设备":      "Disconnected",
	"同步已暂停":      "Sync Paused",
	"已暂停同步":      "Sync paused",
	"未连接":        "Disconnected",
	"已连接":        "Connected",
	"已确认写入对端剪贴板": "Confirmed written to peer clipboard",
	"写入或清理历史失败":  "Failed to write or clean up history",
	"未知设备":       "Unknown Device",
	"空文本":        "Empty Text",
	"未知系统":       "Unknown OS",
	"局域网":        "LAN",
	"语言":         "Language",
	"界面语言":       "Interface Language",
	"界面主题":       "Appearance",
	"浅色":         "Light",
	"深色":         "Dark",
	"跟随系统":       "Follow System",

	// Tray & Menu Bar Icon
	"菜单栏图标": "Menu Bar Icon",
	"托盘图标":  "Tray Icon",
	"图标样式":  "Icon Style",
	"邮筒":    "Mailbox",
	"小鸟":    "Bird",
	"信封":    "Envelope",
	"保存图标失败，选择未生效，请检查数据目录权限后重试":    "Failed to save icon; check data directory permissions and try again",
	"图标已保存，但切换失败，请重启 Snapover 后重试": "Icon saved, but switching failed; restart Snapover and try again",
	"已暂停": "Paused",
	"等待忽略下一次复制（不记录、不同步）": "Ignoring next copy (won't record or sync)",
	"快速历史":      "Quick History",
	"检查更新…":     "Check for Updates…",
	"取消忽略下一次复制": "Cancel Ignore Next Copy",
	"暂停同步":      "Pause Sync",
	"开机启动":      "Launch at Login",
	"退出":        "Quit",
	"忽略下一次复制":   "Ignore Next Copy",
	"该次复制不记录历史，也不同步": "The next copy won't be saved to history or synced",
	"等待复制…":     "Waiting for copy…",
	"取消":        "Cancel",
	"忽略一次":      "Ignore Once",
	"启用忽略下一次复制": "Enable Ignore Next Copy",
	"无法忽略下一次复制，请稍后重试":                  "Cannot ignore next copy, please try again",
	"等待下一次复制，该次内容不记录、不同步":              "Waiting for next copy; it won't be recorded or synced",
	"已取消忽略下一次复制":                       "Cancelled ignoring next copy",
	"已设置开机启动":                          "Launch at login enabled",
	"已关闭开机启动":                          "Launch at login disabled",
	"开机启动需要用打好的 Snapover.app，源码直接运行不行": "Launch at login requires the packaged Snapover.app",
	"到系统设置的登录项里允许 Snapover":            "Allow Snapover in System Settings → Login Items",
	"开机启动没改成":                          "Failed to change launch at login",

	// Settings Page
	"通用与外观":  "General & Appearance",
	"剪贴板同步":  "Clipboard Sync",
	"只同步文本":  "Text Only",
	"大小限制":   "Size Limits",
	"文本大小上限": "Max Text Size",
	"1–20 MB，超限不保存、不收发。":  "1–20 MB; items over the limit are not saved or synced.",
	"文本大小上限，MB":           "Max text size, MB",
	"应用":                  "Apply",
	"应用文本上限":              "Apply max text size",
	"图片大小上限":              "Max Image Size",
	"最多 20 MB，超限仅保留本机历史。": "Up to 20 MB; larger images are kept in local history only.",
	"图片大小上限，MB":           "Max image size, MB",
	"文件复制同步上限":            "File Clipboard Limit",
	"默认 20 MB，整批文件和文件夹合计；仅限制复制同步，不影响主动发送。": "Default 20 MB total for copied files and folders; does not limit explicit file sends.",
	"文件复制同步上限，MB":               "File clipboard limit, MB",
	"应用文件复制同步上限":                "Apply file clipboard limit",
	"文本上限保存失败，请检查数据目录权限后重试":     "Failed to save text limit; check data directory permissions and try again",
	"文件复制同步上限须为有效的正整数，单位 MB":    "File clipboard limit must be a positive integer in MB",
	"文件复制同步上限保存失败，请检查数据目录权限后重试": "Failed to save file clipboard limit; check data directory permissions and try again",
	"保存设置失败，设置未生效，请检查数据目录权限后重试": "Failed to save settings; check data directory permissions and try again",
	"保存设置失败":        "Failed to save settings",
	"复制并同步快捷键":      "Copy & Sync Shortcut",
	"快速历史快捷键":       "Quick History Shortcut",
	"历史存储":          "History Storage",
	"防止自动锁屏":        "Prevent Lock Screen",
	"跨网络连接":         "Cross-Network Connection",
	"文件同步":          "File Sync",
	"接收文件夹":         "Receive Folder",
	"本机与诊断":         "This Device & Diagnostics",
	"设备信息":          "Device Info",
	"名称":            "Name",
	"设备 ID":         "Device ID",
	"证书指纹":          "Certificate Fingerprint",
	"地址":            "Address",
	"没有可用的局域网 IPv4": "No LAN IPv4 address available",
	"连接诊断":          "Connection Diagnostics",
	"测试连接":          "Test Connection",
	"关于与更新":         "About & Updates",

	// Sync Mode & Copy Shortcut
	"同步方式": "Sync Mode",
	"自动":   "Auto",
	"手动":   "Manual",
	"普通复制仅保留在本机，按“复制并同步”快捷键发送。仍可接收对端内容。":           "Normal copy stays local; press the \"Copy & Sync\" shortcut to send. Incoming items are still received.",
	"同步方式保存失败，请检查数据目录权限后重试":                        "Failed to save sync mode; check data directory permissions and try again",
	"请先在其他应用中选中要复制的内容":                             "Select content to copy in another app first",
	"请松开快捷键后再试":                                    "Release the shortcut keys and try again",
	"当前应用已切换，请重新复制并同步":                             "Active application changed; please copy and sync again",
	"未复制到新内容，请选中文本、图片或文件后再试":                       "No new content copied; select text, an image, or a file and try again",
	"复制处理超时，已保留在本机历史，请稍后查看":                        "Copy timed out; saved to local history",
	"请在系统设置 → 隐私与安全性 → 辅助功能中允许 Snapover，然后重新复制并同步": "Allow Snapover in System Settings → Privacy & Security → Accessibility, then try again",
	"复制并同步失败":              "Copy & Sync Failed",
	"请保留普通复制快捷键，另选一组组合键":   "Keep the standard copy shortcut and choose a different key combination",
	"此快捷键已用于快速历史，请另选一组":    "This shortcut is already used for Quick History; choose another",
	"复制并同步快捷键无法注册，请在设置中更换": "Failed to register Copy & Sync shortcut; change it in Settings",
	"复制并同步快捷键已更新":          "Copy & Sync shortcut updated",

	// Shortcut Recorder & Picker Shortcut
	"修饰键请使用 Ctrl、Alt、Shift、Cmd 或 Win":  "Use Ctrl, Alt, Shift, Cmd, or Win as modifier keys",
	"快捷键中有重复的修饰键":                      "Shortcut contains duplicate modifier keys",
	"请至少使用 Ctrl、Alt、Cmd 或 Win 中的一个修饰键": "Include at least one of Ctrl, Alt, Cmd, or Win",
	"主键请使用字母、数字、F1–F12、Space 或方向等常用键":  "Use letters, digits, F1–F12, Space, or arrow keys as the main key",
	"请使用字母、数字、F1–F12 或方向等常用键":          "Use letters, digits, F1–F12, or arrow keys",
	"快捷键保存失败，请检查数据目录权限后重试":             "Failed to save shortcut; check data directory permissions and try again",
	"此快捷键已用于复制并同步，请另选一组":               "This shortcut is already used for Copy & Sync; choose another",
	"快速历史快捷键已更新":                       "Quick History shortcut updated",
	"请按下快捷键…":                          "Press shortcut…",
	"设置快速历史快捷键":                        "Set Quick History Shortcut",
	"设置复制并同步快捷键":                       "Set Copy & Sync Shortcut",
	"恢复默认":                             "Reset Default",
	"恢复默认快速历史快捷键":                      "Reset Default Quick History Shortcut",
	"恢复默认复制并同步快捷键":                     "Reset Default Copy & Sync Shortcut",
	"保存":                               "Save",
	"保存快速历史快捷键":                        "Save Quick History Shortcut",
	"保存复制并同步快捷键":                       "Save Copy & Sync Shortcut",
	"取消设置快速历史快捷键":                      "Cancel Setting Quick History Shortcut",
	"取消设置复制并同步快捷键":                     "Cancel Setting Copy & Sync Shortcut",

	// History Storage & Disk Usage
	"容量上限":      "Capacity Limit",
	"历史容量上限，MB": "History capacity limit, MB",
	"应用并清理":     "Apply & Clean",
	"已用":        "Used",
	"正在统计占用…":   "Calculating usage…",
	"刷新":        "Refresh",
	"刷新占用":      "Refresh usage",
	"置顶内容已超过容量上限，请取消部分置顶或手动删除。": "Pinned items exceed the capacity limit; unpin or delete some items.",
	"正在清理历史…": "Cleaning up history…",
	"历史内容占用":  "History content usage",
	"历史容量上限须为 1–1048576 的整数，单位 MB": "History capacity limit must be an integer between 1 and 1048576 MB",
	"历史上限保存失败，请检查数据目录权限后重试":        "Failed to save history limit; check data directory permissions and try again",
	"历史容量上限已应用，超限的未置顶记录已清理":        "History capacity limit applied; excess unpinned items cleaned up",
	"已清理全部未置顶历史":                   "Cleared all unpinned history",
	"正在处理历史…":                      "Processing history…",
	"历史操作未完成，请检查数据目录权限后重试":         "History operation failed; check data directory permissions and try again",
	"无法统计历史占用，请检查数据目录权限后刷新":        "Failed to calculate history usage; check data directory permissions and refresh",
	"占用明细":        "Storage Breakdown",
	"置顶内容":        "Pinned Items",
	"已包含在历史内容占用中": "Included in history content usage",
	"历史目录":        "History Directory",
	"包含历史内容、索引、日志和临时查看副本":            "Includes history content, index, logs, and temporary preview files",
	"正在统计文件与备份占用…":                   "Calculating file and backup usage…",
	"文件剪贴板缓存":                        "File Clipboard Cache",
	"损坏历史备份":                         "Corrupted History Backups",
	"打开接收文件夹":                        "Open Receive Folder",
	"整理历史备份":                         "Manage History Backups",
	"打开数据目录，查看 history.damaged-* 备份": "Open data directory to view history.damaged-* backups",
	"打开历史备份位置失败，请从文件管理器打开数据目录":       "Failed to open backup folder; open the data directory from your file manager",
	"统计失败":                           "Failed",
	"历史不可用，连接、发送和设置仍可使用。":            "History is unavailable; connection, sending, and settings still work.",
	"损坏文件会原样保留，确认后才移到一旁并重建。":         "Corrupted files are preserved and moved aside before rebuilding.",
	"移到一旁并重建":                        "Quarantine & Rebuild",
	"无法移开损坏历史":                       "Failed to quarantine corrupted history",
	"重建历史失败":                         "Failed to rebuild history",
	"历史上限未能应用":                       "Failed to apply history limit",

	// Keep Awake & Updates
	"已暂停，返回当前用户桌面后恢复。":                       "Paused; resumes when returning to the active desktop.",
	"已停止保持唤醒，但关闭状态未保存，下次启动可能重新开启。请检查数据目录权限。": "Keep awake stopped, but setting could not be saved. Check data directory permissions.",
	"防止自动锁屏未开启": "Failed to enable Prevent Lock Screen",
	"保持唤醒设置保存失败，设置未更改，请检查数据目录权限后重试。": "Failed to save Prevent Lock Screen setting; check data directory permissions and try again.",
	"这个版本不能自动更新，请从发布页下载新版":           "Auto-update is unavailable in this build; download the latest release manually",
	"检查更新":   "Check for Updates",
	"自动检查更新": "Check for Updates Automatically",
	"每 24 小时在后台检查一次，有新版本时提示。":                "Check every 24 hours in the background and notify when an update is available.",
	"这个版本不能自动更新：开发构建，或安装位置不可写（例如直接从磁盘映像运行）。": "Auto-update is unavailable: development build or read-only install location (e.g. running from disk image).",
	"还没有检查过更新。": "Never checked for updates.",

	// Devices Page & Connections & Segments
	"添加设备":    "Add Device",
	"正在添加设备":  "Adding device",
	"取消添加":    "Cancel",
	"已取消添加设备": "Cancelled adding device",
	"核对校验码":   "Verify Pairing Code",
	"确认配对":    "Confirm Pairing",
	"等待对方确认":  "Waiting for Peer",
	"拒绝配对":    "Decline",
	"核对两台电脑的校验码后确认配对": "Verify the code on both computers and confirm pairing",
	"已确认，等待另一台电脑确认":   "Confirmed; waiting for the other computer to confirm",
	"已配对设备":       "Paired Devices",
	"离线":          "Offline",
	"正在重连":        "Reconnecting",
	"连接":          "Connect",
	"详情":          "Details",
	"再次添加需要双方确认。": "Adding again will require confirmation on both computers.",
	"确认解除配对":      "Confirm Unpair",
	"解除配对":        "Unpair",
	"解除配对失败，请重试":  "Failed to unpair, please try again",
	"已解除配对并断开连接。再次添加需要双方确认": "Unpaired and disconnected. Adding again requires confirmation on both sides",
	"附近的设备":              "Nearby Devices",
	"配对":                 "Pair",
	"正在连接":               "Connecting",
	"暂未发现其他电脑":           "No other computers found yet",
	"通过地址连接":             "Connect by Address",
	"连接方式":               "Connection Route",
	"192.168.1.20，端口可省略": "192.168.1.20 (port optional)",
	"100.x.y.z，端口可省略":    "100.x.y.z (port optional)",
	"两端需开启 Tailscale 连接，首次配对仍需双方核对校验码。": "Both sides must enable Tailscale connection; first-time pairing still requires code verification.",
	"对端地址":      "Peer Address",
	"连接地址":      "Connect",
	"广播网段与配对管理": "Broadcast Subnets & Pairing",
	"解除全部配对":    "Unpair All",
	"确认解除全部配对":  "Confirm Unpair All",
	"再次添加任何设备都需要双方确认。":        "Adding any device again will require confirmation on both sides.",
	"解除全部配对失败，请重试":            "Failed to unpair all devices, please try again",
	"已解除全部配对并断开连接。再次添加需要双方确认": "All devices unpaired and disconnected. Adding again requires confirmation on both sides",
	"全选":  "Select All",
	"全不选": "Deselect All",
	"没有可用的局域网网段。连接 Wi-Fi 或网线后会出现在这里。": "No LAN subnets available. Connect to Wi-Fi or Ethernet to see them here.",
	"有新网段未加入广播。":                           "New subnet detected and not included in broadcast.",
	"发现已停止。勾选网段或通过地址连接。":                   "Discovery stopped. Select a subnet or connect by address.",
	"已在全部物理网段广播":                           "Broadcasting on all physical subnets",
	"已停止广播和搜索，仍可手填地址":                      "Broadcast and discovery stopped; manual address connection is still available",
	"已更新广播网段":                              "Updated broadcast subnets",
	"仅用于 Tailscale 地址连接（需开启），不参与发现":        "Tailscale address only (when enabled), not used for discovery",
	"不用于连接":                                "Not used for connections",
	"请填写对端地址，端口可省略":                        "Enter peer address (port optional)",
	"允许通过 Tailscale 连接":                    "Allow Tailscale Connections",
	"保存 Tailscale 设置失败，设置未生效，请检查数据目录权限后重试": "Failed to save Tailscale setting; check data directory permissions and try again",
	"已允许 Tailscale 连接，可在连接设备页添加地址":         "Tailscale connection enabled; add an address on the Devices page",
	"已关闭 Tailscale 连接，已保存的地址仍保留":           "Tailscale connection disabled; saved addresses are retained",
	"局域网地址":        "LAN Address",
	"Tailscale 地址": "Tailscale Address",
	"当前连接":         "Current Route",
	"填写该设备的 Tailscale IPv4，验证设备身份后保存。": "Enter this device's Tailscale IPv4; saved after verifying device identity.",
	"Tailscale IPv4 地址": "Tailscale IPv4 Address",
	"验证并连接":             "Verify & Connect",
	"取消编辑":              "Cancel",
	"添加 Tailscale 地址":   "Add Tailscale Address",
	"修改 Tailscale 地址":   "Edit Tailscale Address",
	"删除 Tailscale 地址":   "Remove Tailscale Address",
	"在设置中开启 Tailscale 连接后，可添加或使用此地址。": "Enable Tailscale connection in Settings to add or use this address.",
	"设备已解除配对，请重新添加设备":                 "Device has been unpaired; please add the device again",
	"删除 Tailscale 地址失败，请检查数据目录权限后重试":  "Failed to remove Tailscale address; check data directory permissions and try again",
	"已删除 Tailscale 地址，设备配对仍保留":        "Removed Tailscale address; device pairing is retained",
	"先填写对端地址": "Enter peer address first",
	"TCP 端口已连通，尚未验证设备身份；同步前仍需完成双方配对确认": "TCP port is reachable (device identity not yet verified; pairing confirmation is still required)",

	// Network Guide
	"允许 Snapover 连接另一台电脑":               "Allow Snapover to connect to another computer",
	"出现防火墙提示时，允许 Snapover 在可信的专用网络上通信。": "When prompted by the firewall, allow Snapover on trusted private networks.",
	"允许 Snapover 查找另一台电脑":               "Allow Snapover to find another computer",
	"系统询问本地网络权限时，选择「允许」。":               "When prompted for Local Network access, choose \"Allow\".",
	"查看连接指引": "Connection Guide",
	"不再提示":   "Don't Show Again",
	"连接帮助":   "Connection Help",
	"保存指引设置失败，请检查数据目录权限后重新关闭指引重试": "Failed to save guide setting; check data directory permissions and try again",
	"连接指引 · Snapover": "Connection Guide · Snapover",
	"连接另一台电脑":         "Connect to Another Computer",
	"正在搜索同一网络内的电脑…":   "Searching for computers on the same network…",
	"重新搜索":            "Search Again",
	"返回连接设备":          "Back to Devices",
	"仍然无法连接？":         "Still can't connect?",
	"请在两台电脑上分别检查。访客 Wi-Fi 或网络隔离可能阻止电脑互相连接。":     "Check on both computers. Guest Wi-Fi or AP isolation may block peer-to-peer connections.",
	"可以在「连接设备」中手填对端地址，绕过自动发现；仍需允许本地网络访问和防火墙连接。": "You can manually enter the peer address in \"Devices\" to bypass auto-discovery; Local Network permission and firewall access are still required.",
	"可以在「连接设备」中手填对端地址，绕过自动发现；仍需防火墙允许连接。":        "You can manually enter the peer address in \"Devices\" to bypass auto-discovery; firewall access is still required.",
	"关闭指引":      "Close Guide",
	"查看防火墙设置方法": "How to Configure Firewall",
	"1. 打开防火墙设置，选择「允许应用或功能通过 Windows Defender 防火墙」。":       "1. Open Firewall settings and select \"Allow an app or feature through Windows Defender Firewall\".",
	"2. 点击「更改设置」，找到 Snapover，勾选应用及「专用」网络。":                 "2. Click \"Change settings\", find Snapover, and check the app and \"Private\" network.",
	"3. 如果列表中没有 Snapover，点击「允许其他应用」，选择正在使用的 Snapover.exe。": "3. If Snapover is not listed, click \"Allow another app\" and select Snapover.exe.",
	"更改设置可能需要管理员权限。设置后返回此窗口，会立即重新搜索。":                      "Changing settings may require administrator privileges. Return to this window afterwards to search again.",
	"打开防火墙设置": "Open Firewall Settings",
	"如果当前网络是「公用」，请先确认它是否可信。信任的家庭或办公网络可以改为「专用」，让放行规则与当前网络类型匹配。": "If your network is set to \"Public\", verify that it is trusted. Trusted home or office networks can be switched to \"Private\" to match firewall rules.",
	"打开网络设置":       "Open Network Settings",
	"查看本地网络权限设置方法": "How to Configure Local Network Permission",
	"1. 打开「系统设置 → 隐私与安全性 → 本地网络」。":            "1. Open \"System Settings → Privacy & Security → Local Network\".",
	"2. 找到 Snapover，开启它的本地网络访问。":              "2. Find Snapover and enable Local Network access.",
	"3. 返回这个指引窗口，会立即重新搜索；也可以点击「重新搜索」。":        "3. Return to this guide window to search again automatically, or click \"Search Again\".",
	"如果列表中没有 Snapover，请先点击「重新搜索」，并留意系统的授权弹窗。": "If Snapover is not listed, click \"Search Again\" first and look for the system permission prompt.",
	"若按钮只打开了「隐私与安全性」，请手动选择「本地网络」。":            "If only \"Privacy & Security\" opens, select \"Local Network\" manually.",
	"打开本地网络设置": "Open Local Network Settings",
	"打开系统设置失败，请按上面的步骤手动打开设置后重试": "Failed to open System Settings; please open it manually following the steps above",
	"打开系统设置失败，请按连接帮助中的步骤手动打开":   "Failed to open system settings; follow Connection Help to open them manually",
	"同步文字、图片和文件，不保留字体和颜色。":      "Syncs text, images, and files. Formatting is not kept.",
	"手填地址": "Enter Address",
	"已到对方，未覆盖剪贴板（对方刚复制过）":           "Reached the other computer, but did not replace its clipboard (it just copied)",
	"对方未写入剪贴板":                      "The other computer did not write to the clipboard",
	"本机未写入剪贴板，已存历史":                 "Not written to this clipboard; saved in history",
	"远程桌面剪贴板盖掉了刚才的内容。请关掉该连接的剪贴板共享。": "Remote Desktop clipboard replaced the content. Turn off clipboard sharing for that connection.",
	"正在检查文件是否仍可粘贴":                  "Checking whether the files can still be pasted",
	"文件已失效，请重新复制后再置顶":               "Files are gone; copy them again before pinning",
	"已保存置顶副本，源文件删除后仍可粘贴":            "Pinned copy saved; paste still works if the original files are deleted",
	"文件仍在原路径时可粘贴；置顶后会保存副本":          "Paste works while the original path exists; pinning saves a copy",
	"远程桌面剪贴板覆盖了写入结果":                "Remote Desktop clipboard overwrote the write",
	"本机跳过，未发送":                      "Skipped locally and not sent",
	"未写入剪贴板":                        "Not written to the clipboard",

	// History Page & Quick Picker & Previews
	"搜索复制历史":      "Search clipboard history",
	"搜索快速历史":      "Search quick history",
	"搜索内容或来源":     "Search content or source",
	"关闭提示":        "Dismiss notification",
	"清空搜索":        "Clear search",
	"清空搜索并查看全部记录": "Clear search and show all items",
	"增加":          "Increase",
	"减少":          "Decrease",
	"清空未置顶":       "Clear Unpinned",
	"确认清空":        "Confirm Clear",
	"全部":          "All",
	"置顶":          "Pinned",
	"文本":          "Text",
	"图片":          "Images",
	"文件":          "Files",
	"历史类型":        "History Category",
	"还没有复制历史":     "No clipboard history yet",
	"复制文字、图片、文件或文件夹后会出现在这里": "Copied text, images, files, or folders will appear here",
	"没有匹配的记录":  "No matching items",
	"没有匹配的记录。": "No matching items.",
	"还没有记录":    "No items yet",
	"正在搜索…":    "Searching…",
	"搜索中…":     "Searching…",
	"搜索失败":     "Search failed",
	"本机":       "Local",
	"同步":       "Synced",
	"对端":       "Peer",
	"刚刚":       "Just now",
	"复制":       "Copy",
	"取消置顶":     "Unpin",
	"置顶当前记录":   "Pin selected item",
	"取消置顶当前记录": "Unpin selected item",
	"删除":       "Delete",
	"记录操作":     "Item Actions",
	"已更新置顶":    "Updated pin state",
	"已删除":      "Deleted",
	"已写回剪贴板":   "Copied to clipboard",
	"在快速历史内直接粘贴此条记录":                                      "Paste this item directly in Quick History",
	"首次粘贴需要在辅助功能里允许 Snapover":                             "First-time paste requires allowing Snapover in Accessibility",
	"快速历史窗口大小没能保存，请检查数据目录权限后重试":                           "Failed to save Quick History window size; check data directory permissions",
	"已复制。要直接粘贴，请在系统设置 → 隐私与安全性 → 辅助功能里允许 Snapover，然后再试一次": "Copied. To paste directly, allow Snapover in System Settings → Privacy & Security → Accessibility and try again",
	"已复制。刚才的窗口已经不在了，请再按一次粘贴":                              "Copied. The target window is no longer available; press paste manually",
	"已复制，但没能把焦点还给刚才的窗口":                                   "Copied, but could not restore focus to the previous window",
	"已复制。没有记住刚才的窗口，请在目标处再按一次粘贴":                           "Copied. Previous window was not captured; paste manually in the target app",
	"正在处理历史，请稍后再试":                                        "Processing history, please try again shortly",
	"记录已删除，请重新选择":                                         "Item was deleted, please select again",
	"粘贴":                                                  "Paste",
	"只复制":                                                 "Copy Only",
	"在 Finder 中显示":                                        "Show in Finder",
	"在资源管理器中显示":                                           "Show in File Explorer",
	"文件正文未保存在历史中，请重新复制":                                   "File contents are not stored in history; please copy the file again",
	"文件内容未保存在历史中，请重新复制后再粘贴":                               "File contents are not stored in history; please copy the file again before pasting",
	"正在清理历史，请稍后再试":                                        "Cleaning up history, please try again shortly",
	"正在准备粘贴…":                                             "Preparing to paste…",
	"正在准备复制…":                                             "Preparing to copy…",
	"正在读取历史…":                                             "Reading history…",
	"读历史失败":                                               "Failed to read history",
	"写回剪贴板失败":                                             "Failed to write to clipboard",
	"图片预览加载失败，请重新悬停重试":                                    "Failed to load image preview; hover again to retry",
	"文本预览加载失败，请重新悬停重试":                                    "Failed to load text preview; hover again to retry",
	"文件预览加载失败，请重新悬停重试":                                    "Failed to load file preview; hover again to retry",
	"查看全文":      "Full Text",
	"图片预览":      "Image Preview",
	"放大查看":      "View Larger",
	"缩小":        "Zoom Out",
	"放大":        "Zoom In",
	"适应窗口":      "Fit to Window",
	"实际大小":      "Actual Size",
	"用系统应用打开":   "Open in Default App",
	"用系统应用打开原图": "Open Original in Default App",
	"相对图片原始像素":  "Relative to the image's own pixels",
	"正在加载图片…":   "Loading image…",
	"图片读取失败，请重新复制后再试": "Failed to read the image; copy it again and retry",
	"用系统应用打开失败，请重试":   "Could not open the default app; try again",
	"关闭":    "Close",
	"正在加载…": "Loading…",
	"原图打开失败，请重新选择图片后重试":     "Failed to open original image; select the image and try again",
	"原图查看缓存已满，请等待 15 分钟后重试": "Image preview cache is full; please wait 15 minutes and try again",
	"文本预览": "Text Preview",
	"文本全文": "Full Text",
	"全文加载失败，请重新选择文本后重试": "Failed to load full text; select the text and try again",
	"上一段":     "Previous",
	"下一段":     "Next",
	"复制全文":    "Copy All",
	"└─ 空文件夹": "└─ Empty folder",
	"还有更多项目 · 仅展示一层，最多 20 项": "More items omitted · showing 1 level, up to 20 items",
	"仅展示一层，最多 20 项":          "Showing 1 level, up to 20 items",
	"文字":                     "Text",
	"文件夹":                    "Folder",
	"路径":                     "Path",
	"内容":                     "Content",
	"来源":                     "Source",
	"来源：":                    "Source:",
	"按来源筛选：":                 "Search this source: ",
	"备注":                     "Note",
	"置顶备注":                   "Pinned Note",
	"输入备注或别名，便于搜索":           "Enter a note or alias for quick search",
	"设置备注":                   "Add Note",
	"修改备注":                   "Edit Note",
	"设置当前记录备注":               "Edit note for selected item",
	"设置备注…":                  "Add Note…",
	"修改备注…":                  "Edit Note…",
	"保存备注":                   "Save Note",
	"清除备注":                   "Clear Note",
	"取消编辑备注":                 "Cancel editing note",
	"已保存备注":                  "Note saved",
	"已清除备注":                  "Note cleared",
	"请先置顶记录后再设置备注":           "Pin the item before adding a note",
	"来源应用":                   "Source App",
	"来源设备":                   "Source Device",
	"文件名":                    "Filename",
	"目录":                     "Directory",
	"类型":                     "Type",
	"时间":                     "Time",
	"大小":                     "Size",
	"尺寸":                     "Dimensions",

	// Send File Window & File Transfer
	"发送文件 · Snapover": "Send File · Snapover",
	"发送文件":            "Send File",
	"把文件拖到这里":         "Drop files here",
	"选择文件…":           "Choose Files…",
	"更换…":             "Change…",
	"文件发送进度":          "File transfer progress",
	"文件同步进度":          "File sync progress",
	"文件同步 · Snapover": "File Sync · Snapover",
	"对方处理中…":          "Waiting for the other computer…",
	"文件同步失败":          "File sync failed",
	"连接设备后即可发送":       "Connect a device to send files",
	"对方暂不支持接收文件，请更新对方的 Snapover": "Peer does not support receiving files; update Snapover on the peer",
	"可多选文件，大小仅受接收磁盘限制":           "Select multiple files; size is limited only by the receiving disk",
	"暂停":    "Pause",
	"继续发送":  "Resume",
	"取消传输":  "Cancel transfer",
	"已取消传输": "Transfer cancelled",
	"传输已停止，但无法确认对方是否已保存，请检查对方接收文件夹": "Transfer stopped, but the save could not be confirmed. Check the peer's receive folder.",
	"已暂停，点击继续发送可从当前位置恢复":            "Paused. Resume to continue from this position.",
	"连接已断开，正在等待原设备重连后续传…":           "Disconnected. Waiting for the original device to reconnect…",

	"关闭窗口后仍会继续传输": "Transfer continues in the background if you close this window",
	"完成":          "Done",
	"发送":          "Send",
	"请选择要发送的文件":   "Choose files to send",
	"选择要发送的文件":    "Choose files to send",
	"选择文件失败":      "Failed to choose file",
	"正在等待对方接收…":   "Waiting for peer to accept…",
	"传输完成，正在等待对方确认保存…": "Transfer finished; waiting for peer to confirm save…",
	"已发送":               "Sent",
	"文件已发送 · Snapover":  "File Sent · Snapover",
	"文件发送失败 · Snapover": "File Send Failed · Snapover",
	"传输已取消 · Snapover":  "Transfer Cancelled · Snapover",
	"收到文件 · Snapover":   "File Received · Snapover",
	"接收失败":              "Receive failed",
	"更改接收文件夹…":          "Change Receive Folder…",
	"选择接收文件夹":           "Choose Receive Folder",
	"选择接收文件夹失败":         "Failed to choose receive folder",
	"保存接收位置失败":          "Failed to save receive folder",
	"已更改接收文件夹，下一次接收生效":  "Receive folder updated (takes effect on next transfer)",
	"打开文件夹":             "Open Folder",
	"正在发送另一批文件，请完成后再发送": "Another file transfer is in progress; please wait until it finishes",
	"当前在线设备已改变，请确认设备后点击「发送」":  "Connected device changed; verify the device and click \"Send\"",
	"没有在线设备。请先连接设备，再点击「发送」":   "No device online. Connect a device first, then click \"Send\"",
	"正在连接最近的设备，连接成功后会自动发送…":   "Connecting to recent device; will send automatically once connected…",
	"连接或文件已改变，请确认后点击「发送」":     "Connection or files changed; verify and click \"Send\"",
	"暂时未能连接设备。请先连接设备，再点击「发送」": "Could not connect to device in time. Connect first, then click \"Send\"",
	"Finder 服务":                       "Finder Service",
	"Windows「发送到」入口":                  "Windows \"Send to\" Menu",
	"注册 Finder 服务失败":                  "Failed to register Finder service",
	"请在 Snapover 设置中启用 Finder 发送文件服务": "Enable the Finder Send File service in Snapover Settings first",
	"允许从 Finder 发送文件":                 "Allow Sending Files from Finder",
	"右键文件 → 服务 → Snapover 发送文件。菜单是否显示可在系统设置的「服务」中调整。": "Right-click file → Services → Send with Snapover. Visibility can be managed in System Settings → Services.",
	"刷新 Finder 服务":                 "Refresh Finder Service",
	"在 Windows「发送到」菜单中显示 Snapover": "Show Snapover in Windows \"Send to\" menu",

	// Internal discovery / lan / sync / filetransfer messages
	"没有可用的局域网网段，连接 Wi-Fi 或网线后自动恢复": "No LAN subnets available; will resume automatically when Wi-Fi or Ethernet connects",
	"局域网发现不可用，可通过地址连接":             "LAN discovery is unavailable; connect by address instead",
	"对端地址为空":                           "Peer address is empty",
	"请填写 IPv4 地址":                      "Enter an IPv4 address",
	"端口须为 1–65535":                     "Port must be between 1 and 65535",
	"地址不在局域网私网或链路本地网段内":                "Address is not within a private LAN or link-local subnet",
	"Tailscale 地址须在 100.64.0.0/10 网段内": "Tailscale address must be within 100.64.0.0/10",
	"请先在设置中开启 Tailscale 连接":            "Enable Tailscale connection in Settings first",
	"监听端口无法用于 Tailscale 连接，请勿绑定到仅局域网的单一地址": "Listen address cannot accept Tailscale connections; do not bind to a LAN-only IP",
	"未连接对端":     "Not connected to peer",
	"对端已断开":     "Peer disconnected",
	"对端未确认接收":   "Peer did not confirm receipt",
	"跳过":        "Skipped",
	"未接收":       "Not received",
	"已暂停同步，未发送": "Sync is paused; not sent",
	"开启了只同步文本，图片未发送":     "Text-only mode is enabled; image not sent",
	"对端开启了只同步文本，未接收图片":   "Peer has text-only mode enabled and did not accept the image",
	"对端已暂停同步":            "Peer has paused sync",
	"对端已暂停同步，未接收":        "Peer has paused sync and did not accept the item",
	"对端刚复制了新内容，未覆盖对端剪贴板": "Peer just copied new content; peer clipboard was not overwritten",
	"本机刚复制了新内容，已保留本机剪贴板": "Local clipboard was just updated; kept local clipboard",
	"密码或隐私剪贴板内容不会同步":     "Password or concealed clipboard content is not synced",
	"历史不可用，其他功能仍可使用":     "History is unavailable; other features still work",
	"历史维护失败，请在设置中重新应用上限": "History maintenance failed; re-apply the limit in Settings",
	"CLI 服务启动失败":         "Failed to start CLI service",
}
