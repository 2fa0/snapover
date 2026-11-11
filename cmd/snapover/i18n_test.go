package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/discovery"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

func TestLanguageFromTags(t *testing.T) {
	tests := []struct {
		tags []string
		want string
	}{
		{nil, settings.LangZH},
		{[]string{}, settings.LangZH},
		{[]string{"zh-Hans-CN", "en-US"}, settings.LangZH},
		{[]string{"zh_TW"}, settings.LangZH},
		{[]string{"en-US", "zh-Hans"}, settings.LangEN},
		{[]string{"en"}, settings.LangEN},
		{[]string{"fr-FR", "zh-Hans"}, settings.LangZH},
		{[]string{"fr-FR", "de-DE"}, settings.LangEN},
	}
	for _, tc := range tests {
		if got := languageFromTags(tc.tags); got != tc.want {
			t.Fatalf("languageFromTags(%v) = %q, want %q", tc.tags, got, tc.want)
		}
	}
}

func TestLanguageSettingsSwitchAndPersist(t *testing.T) {
	a := interfaceTestApp(t)
	a.systemLang = settings.LangEN
	a.page = pageSettings

	tester := ui.NewTester(a.view, 960, 1400)
	tester.Frame()

	// Follow system (systemLang = en) renders in English
	if _, ok := tester.Find("Clipboard Sync"); !ok {
		t.Fatal("expected English settings heading when following English system locale")
	}
	if _, ok := tester.Find("Interface Language"); !ok {
		t.Fatal("expected Interface Language setting")
	}

	// Switch to 简体中文 manually
	clickInterface(t, tester, "简体中文")
	if a.cfg.Values.Language != settings.LangZH {
		t.Fatalf("language = %q, want %q", a.cfg.Values.Language, settings.LangZH)
	}
	if _, ok := tester.Find("剪贴板同步"); !ok {
		t.Fatal("expected Chinese settings heading after selecting 简体中文")
	}

	// Reload settings from disk to verify persistence
	saved, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Values.Language != settings.LangZH {
		t.Fatalf("persisted language = %q, want %q", saved.Values.Language, settings.LangZH)
	}

	// Switch to English manually
	clickInterface(t, tester, "English")
	if a.cfg.Values.Language != settings.LangEN {
		t.Fatalf("language = %q, want %q", a.cfg.Values.Language, settings.LangEN)
	}
	if _, ok := tester.Find("Clipboard Sync"); !ok {
		t.Fatal("expected English settings heading after selecting English")
	}

	// Switch back to Follow System while systemLang is zh
	a.systemLang = settings.LangZH
	clickInterface(t, tester, "Follow System")
	if a.cfg.Values.Language != settings.LangAuto {
		t.Fatalf("language = %q, want %q", a.cfg.Values.Language, settings.LangAuto)
	}
	if _, ok := tester.Find("剪贴板同步"); !ok {
		t.Fatal("expected Chinese UI when Follow System is selected and systemLang is zh")
	}
}

func TestEnglishUISurfaces(t *testing.T) {
	a := interfaceTestApp(t)
	a.cfg.Values.Language = settings.LangEN

	// 1. Devices page
	a.page = pageDevices
	a.discovered = []discovery.Device{{ID: "peer-1", Name: "Office-PC", Platform: "windows", Addr: "192.168.1.20:47821"}}
	tester := ui.NewTester(a.view, 960, 900)
	tester.Frame()
	for _, want := range []string{"Settings", "Devices", "History", "Add Device", "Nearby Devices", "Pair", "Connect by Address"} {
		if _, ok := tester.Find(want); !ok {
			t.Fatalf("devices page missing %q", want)
		}
	}

	// 2. History page (empty & populated)
	a.page = pageHistory
	tester.Frame()
	if _, ok := tester.Find("No clipboard history yet"); !ok {
		t.Fatal("expected English empty history state")
	}
	item, _ := clip.FromText("hello world")
	item.SourceApp = "Safari"
	if _, err := a.hist.Add(history.DirRemote, "MacBook", item); err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	tester.Frame()
	for _, want := range []string{"Clear Unpinned", "All", "Pinned", "1 items", "Synced", "Just now"} {
		if _, ok := tester.Find(want); !ok {
			t.Fatalf("history page missing %q", want)
		}
	}

	// 3. Quick History Picker & Search Match Field
	a.pickQuery = "Safari"
	pickTester := ui.NewTester(a.pickerView, 380, 440)
	pickTester.Frame()
	if _, ok := pickTester.Find("Search quick history"); !ok {
		t.Fatal("picker missing English search input label")
	}
	if !pickTester.HasText("Source App: Safari") {
		t.Fatalf("picker missing translated match field: %v", pickTester.Texts())
	}

	// 4. Send File Window
	fileTester := ui.NewTester(a.filesView, 460, 340)
	fileTester.Frame()
	for _, want := range []string{"Send File", "Drop files here", "Choose Files…", "Connect a device to send files"} {
		if _, ok := fileTester.Find(want); !ok {
			t.Fatalf("files view missing %q", want)
		}
	}

	// 5. Network Guide Window
	g := &networkGuide{app: a, kind: guideMacLocalNetwork, searchStarted: time.Now()}
	guideTester := ui.NewTester(g.view, 600, 620)
	guideTester.Frame()
	for _, want := range []string{"Connect to Another Computer", "Found 1 computer(s)", "Search Again", "Close Guide"} {
		if _, ok := guideTester.Find(want); !ok {
			t.Fatalf("network guide missing %q", want)
		}
	}

	// 6. Tray menu & macOS App menu
	trayItems := a.trayMenu().Items()
	if len(trayItems) == 0 || trayItems[0].Label != "Quick History" {
		t.Fatalf("unexpected tray menu first item: %+v", trayItems)
	}
	appItems := a.windowAppMenu().Items()
	if len(appItems) == 0 || len(appItems[0].Submenu) < 3 || appItems[0].Submenu[2].Label != "Settings…" {
		t.Fatalf("unexpected window app menu: %+v", appItems)
	}
}

func TestTranslateENPatterns(t *testing.T) {
	tests := map[string]string{
		"已连接 · 局域网":                             "Connected · LAN",
		"已通过局域网连接 MacBook":                      "Connected to MacBook via LAN",
		"正在通过Tailscale连接 100.64.0.2:47821":      "Connecting to 100.64.0.2:47821 via Tailscale",
		"已收到来自 Office 的文字":                      "Received text from Office",
		"已发送图片到 MacBook":                        "Sent image to MacBook",
		"跳过：文本超过 5 MB 上限":                       "Skipped: Text exceeds 5 MB limit",
		"未接收：对端已暂停同步":                           "Not received: Peer has paused sync",
		"图片 1920×1080":                          "Image 1920×1080",
		"文件夹 · docs（12 项）":                      "Folder · docs (12 items)",
		"文件 · a.txt 等 3 项":                      "Files · a.txt +3 items",
		"5 分钟前":                                 "5m ago",
		"192.168.1.0/24 · en0 · 本机 192.168.1.5": "192.168.1.0/24 · en0 · Local 192.168.1.5",
	}
	for in, want := range tests {
		if got := translateEN(in); got != want {
			t.Fatalf("translateEN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTranslateENFileBatchResult(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"已发送 1/2 个文件。已取消传输", "Sent 1/2 files. Transfer cancelled"},
		{"已发送 1/2 个文件。传输已停止，但无法确认对方是否已保存，请检查对方接收文件夹", "Sent 1/2 files. Transfer stopped, but the save could not be confirmed. Check the peer's receive folder."},
	} {
		if got := translateEN(tc.input); got != tc.want {
			t.Fatalf("translateEN(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestEnglishHistoryPreservesUserContent(t *testing.T) {
	a := interfaceTestApp(t)
	a.cfg.Values.Language = settings.LangEN
	texts := []string{"设置", "名称：项目A", "图片 文档", "空文本"}
	for _, text := range texts {
		item, _ := clip.FromText(text)
		if _, err := a.hist.Add(history.DirRemote, "同步", item); err != nil {
			t.Fatal(err)
		}
	}
	a.refreshEntries()
	a.page = pageHistory
	for _, view := range []func(*ui.Context){a.view, a.pickerView} {
		tester := ui.NewTester(view, 960, 900)
		tester.Frame()
		for _, text := range append(texts, "同步") {
			if !tester.HasText(text) {
				t.Fatalf("missing original content %q: %v", text, tester.Texts())
			}
		}
	}
	for _, query := range []string{"设置", "同步"} {
		a.pickQuery = query
		tester := ui.NewTester(a.pickerView, 380, 440)
		tester.Frame()
		if !tester.HasText("设置") {
			t.Fatalf("search translated clipboard title: %v", tester.Texts())
		}
	}
	a.page = pageDevices
	a.discovered = []discovery.Device{{ID: "peer", Name: "设置", Platform: "windows"}}
	tester := ui.NewTester(a.view, 960, 900)
	tester.Frame()
	if !tester.HasText("设置") {
		t.Fatalf("discovered device name was translated: %v", tester.Texts())
	}
}

func TestHistoryTitleTranslatesOnlyMetadata(t *testing.T) {
	a := interfaceTestApp(t)
	a.cfg.Values.Language = settings.LangEN
	for _, tc := range []struct {
		entry history.Entry
		want  string
	}{
		{history.Entry{Kind: string(clip.KindText), Text: "设置"}, "设置"},
		{history.Entry{Kind: string(clip.KindText), Text: "空文本"}, "空文本"},
		{history.Entry{Kind: string(clip.KindText)}, "Empty Text"},
		{history.Entry{Kind: string(clip.KindImage), Width: 100, Height: 50}, "Image  100×50"},
		{history.Entry{Kind: string(clip.KindFiles), FileName: "名称：设置"}, "名称：设置"},
		{history.Entry{Kind: string(clip.KindFiles), FileName: "设置", FileCount: 3}, "设置 (3 items)"},
	} {
		if got := a.entryTitle(tc.entry); got != tc.want {
			t.Fatalf("title = %q, want %q", got, tc.want)
		}
	}
	if got := a.displayName("设置", "peer"); got != "设置" {
		t.Fatalf("device name = %q", got)
	}
	if got := a.displayName("", ""); got != "Unknown Device" {
		t.Fatalf("fallback name = %q", got)
	}
}

func TestEnglishFileTitlesPreserveFileNames(t *testing.T) {
	a := interfaceTestApp(t)
	a.cfg.Values.Language = settings.LangEN
	a.filePaths = []string{"/tmp/设置"}
	if got := a.fileTitle(); got != "设置" {
		t.Fatalf("single file title = %q", got)
	}
	a.filePaths = []string{"/tmp/名称：项目、清单", "/tmp/设置", "/tmp/图片", "/tmp/其他"}
	if got, want := a.fileTitle(), "4 files: 名称：项目、清单, 设置, 图片, etc."; got != want {
		t.Fatalf("batch title = %q, want %q", got, want)
	}
}

func TestNativeLanguageFollowsSettings(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("set SNAPOVER_TEST_UI=1 to test native language changes")
	}
	a := interfaceTestApp(t)
	mygo.RunOnMain(func() {
		previous, languages := mygo.App.Locale(), mygo.App.Languages()
		defer func() {
			mygo.App.SetLanguages(languages...)
			mygo.App.SetLocale(previous)
		}()
		off := mygo.App.OnLocaleChanged(a.localeChanged)
		defer off()
		a.root = context.Background()
		configureLanguage(a.cfg.Values.Language)
		for _, tc := range []struct {
			choice int
			lang   string
			menu   string
		}{
			{1, settings.LangZH, "编辑"},
			{2, settings.LangEN, "Edit"},
		} {
			a.saveLanguageChoice(tc.choice)
			if got := mygo.App.Locale(); got != tc.lang {
				t.Errorf("MyGo locale = %q, want %q", got, tc.lang)
			}
			if got := a.windowAppMenu().Items()[1].Label; got != tc.menu {
				t.Errorf("native Edit menu = %q, want %q", got, tc.menu)
			}
		}
		a.saveLanguageChoice(0)
		want := i18n.Match(mygo.App.PreferredLanguages(), mygo.App.Languages())
		if want == "" {
			want = settings.LangEN
		}
		if got := mygo.App.Locale(); got != want || a.lang() != want {
			t.Errorf("Follow System: MyGo=%q, app=%q, want %q", got, a.lang(), want)
		}
		if !slices.Contains(mygo.App.Languages(), settings.LangZH) {
			t.Error("Chinese is missing from MyGo's supported languages")
		}
	})
}
