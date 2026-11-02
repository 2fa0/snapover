package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/settings"
	clipsync "github.com/2fa0/snapover/internal/sync"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func interfaceTestApp(t *testing.T) *app {
	t.Helper()
	dir := t.TempDir()
	store, err := history.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	peers, err := clipsync.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := settings.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	engine := &clipsync.Engine{Peers: peers, Name: "测试电脑"}
	engine.SetReceiveDir(dir)
	return newApp(engine, store, cfg, &identity.Identity{}, ":47821", nil, "")
}

func clickInterface(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	if err := tt.Click(text); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryCopyPressPathDoesNotPanic(t *testing.T) {
	for _, reduceMotion := range []bool{false, true} {
		a := interfaceTestApp(t)
		a.board = &pasteTestBoard{}
		item, _ := clip.FromText("待复制")
		if _, err := a.hist.Add(history.DirLocal, "", item); err != nil {
			t.Fatal(err)
		}
		a.refreshEntries()
		a.page = pageHistory
		a.histSel = 0
		tt := ui.NewTester(a.view, 960, 640)
		tt.SetPreferences(ui.Preferences{ReduceMotion: reduceMotion})
		r, ok := tt.Find("复制")
		if !ok {
			t.Fatal("copy button missing")
		}
		x, y := r.X+r.W/2, r.Y+r.H/2
		tt.Press(x, y)
		tt.Frame()
		if a.historyPaste != nil {
			t.Fatal("press started a paste before release")
		}
		tt.Release(x, y)
		tt.Frame()
		clickInterface(t, tt, "设置")
		if a.page != pageSettings {
			t.Fatal("page stayed unresponsive after press")
		}
	}
}

func TestMainNavigationRemainsImmediateDuringMotion(t *testing.T) {
	for _, reduceMotion := range []bool{false, true} {
		a := interfaceTestApp(t)
		tt := ui.NewTester(a.view, 960, 640)
		tt.SetPreferences(ui.Preferences{ReduceMotion: reduceMotion})
		for _, target := range []struct{ label, page string }{
			{"设置", pageSettings},
			{"复制历史", pageHistory},
			{"连接设备", pageDevices},
			{"设置", pageSettings},
		} {
			clickInterface(t, tt, target.label)
			if a.page != target.page {
				t.Fatalf("navigation during animation chose %s, want %s", a.page, target.page)
			}
		}
		if tt.HasText("发送文件…") {
			t.Fatal("main window still exposes the send-file entry")
		}
		if tt.HasText("测试电脑") {
			t.Fatal("sidebar still shows the local device name")
		}
		// 版本号只在设置页的「关于与更新」里出现，不进入侧栏。
		if !tt.HasText("当前版本 " + appVersionLabel()) {
			t.Fatal("settings page does not show the app version")
		}
		tt.Key(0, ui.KeyDown)
		if a.page != pageDevices {
			t.Fatal("sidebar keyboard navigation stopped working")
		}
	}
}

func TestHiddenTitleBarKeepsSidebarAndActionsClearOfWindowControls(t *testing.T) {
	a := interfaceTestApp(t)
	tt := ui.NewTester(a.view, 960, 640)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 78})
	title, ok := tt.Find("Snapover")
	if !ok {
		t.Fatal("app name missing under a hidden title bar")
	}
	if title.Y < 52 {
		t.Fatalf("app name overlaps traffic lights: %+v", title)
	}
	clickInterface(t, tt, "复制历史")
	if a.page != pageHistory {
		t.Fatal("sidebar navigation failed under a hidden title bar")
	}
	a.page = pageHistory
	tt.SetTitleBar(ui.TitleBar{Height: 52, Right: 138})
	clear, ok := tt.Find("清空未置顶")
	if !ok {
		t.Fatal("history actions missing under a hidden title bar")
	}
	if clear.Y < 52 {
		t.Fatalf("history action sits under caption buttons: %+v", clear)
	}
	// The drag strip takes its room from the page, so the actions at the
	// bottom of the page stay inside the window instead of below its edge.
	for _, label := range []string{"搜索复制历史", "历史类型", "复制", "删除"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("%s missing under a hidden title bar", label)
		}
		if r.Y < 52 {
			t.Fatalf("%s sits under the drag strip: %+v", label, r)
		}
		if r.Y+r.H > 640 {
			t.Fatalf("%s reaches past the window's bottom edge: %+v", label, r)
		}
	}
}

func TestHistoryFilterActionsKeepPinnedRecords(t *testing.T) {
	a := interfaceTestApp(t)
	text, _ := clip.FromText("保留的文本")
	pinned, err := a.hist.Add(history.DirLocal, "", text)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.hist.SetPinned(pinned.ID, true); err != nil {
		t.Fatal(err)
	}
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	item, err := clip.FromPNG(pixels.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.hist.Add(history.DirRemote, "其他电脑", item); err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	a.page = pageHistory
	tt := ui.NewTester(a.view, 960, 640)
	clickInterface(t, tt, "图片")
	if len(a.shown()) != 1 || a.histSel != -1 {
		t.Fatal("filter should show only the image with no old selection")
	}
	clickInterface(t, tt, entryTitle(a.shown()[0]))
	clickInterface(t, tt, "删除")
	waitHistoryCleanup(t, a)
	if len(a.shown()) != 0 || len(a.hist.List()) != 1 {
		t.Fatal("deleting a filtered image should leave the pinned text")
	}
	clickInterface(t, tt, "全部")
	clickInterface(t, tt, "保留的文本")
	if entry, ok := a.selected(); !ok || entry.ID != pinned.ID || !tt.HasText("取消置顶") {
		t.Fatal("selection or pin action did not follow the remaining text")
	}
	clickInterface(t, tt, "清空未置顶")
	clickInterface(t, tt, "确认清空")
	waitHistoryCleanup(t, a)
	if entries := a.hist.List(); len(entries) != 1 || entries[0].ID != pinned.ID {
		t.Fatal("clearing should keep the pinned record")
	}
}

func TestSettingsControlsPersistPolicy(t *testing.T) {
	a := interfaceTestApp(t)
	a.page = pageSettings
	tt := ui.NewTester(a.view, 960, 640)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	clickInterface(t, tt, "暂停同步开关")
	clickInterface(t, tt, "只同步文本开关")
	if tt.HasText("图片大小上限，MB") || tt.HasText("文本大小上限，MB") || tt.HasText("文件大小上限，MB") {
		t.Fatal("size limits should stay collapsed until requested")
	}
	if tt.HasText("更改接收文件夹…") {
		t.Fatal("receive folder should stay collapsed until requested")
	}
	clickInterface(t, tt, "大小限制")
	clickInterface(t, tt, "图片大小上限，MB")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("8")
	tt.Key(0, ui.KeyEnter)
	got, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Values.Paused || !got.Values.TextOnly || got.Values.MaxImageMB != 8 {
		t.Fatalf("settings were not saved: %+v", got.Values)
	}
}

func TestTextLimitInputAppliesAndRejectsInvalidValues(t *testing.T) {
	a := interfaceTestApp(t)
	tt := ui.NewTester(a.settingsPage, 960, 1200)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	clickInterface(t, tt, "大小限制")
	clickInterface(t, tt, "文本大小上限，MB")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("8")
	clickInterface(t, tt, "应用文本上限")
	got, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil || got.Values.MaxTextMB != 8 || a.engine.TextLimit() != 8<<20 {
		t.Fatalf("text limit was not applied: %+v %v", got, err)
	}
	for _, value := range []string{"", "abc", "0", "-1", "21", "1.5"} {
		a.maxTextText = value
		a.saveTextLimit()
		if a.maxTextError == "" || a.cfg.Values.MaxTextMB != 8 || a.engine.TextLimit() != 8<<20 {
			t.Fatalf("invalid limit %q changed the setting", value)
		}
	}
	a.maxTextText = " 20 "
	a.saveTextLimit()
	if a.maxTextError != "" || a.maxTextText != "20" || a.engine.TextLimit() != 20<<20 {
		t.Fatal("valid input did not clear the validation error")
	}
	reopened, err := settings.Load(filepath.Dir(a.cfg.Path))
	if err != nil {
		t.Fatal(err)
	}
	b := newApp(a.engine, a.hist, reopened, &identity.Identity{}, ":47821", nil, "")
	if b.maxTextText != "20" || b.engine.TextLimit() != 20<<20 {
		t.Fatal("saved limit was not restored on startup")
	}
}

func TestTextLimitSaveFailureKeepsActiveLimit(t *testing.T) {
	a := interfaceTestApp(t)
	// A directory at the settings file path prevents publishing the new value.
	if err := os.Mkdir(a.cfg.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	a.maxTextText = "8"
	a.saveTextLimit()
	if a.maxTextError == "" || a.cfg.Values.MaxTextMB != 1 || a.engine.TextLimit() != 1<<20 {
		t.Fatal("failed save changed the active limit")
	}
}

func TestPairingCanCancelWithoutTrustingDevices(t *testing.T) {
	a := interfaceTestApp(t)
	tt := ui.NewTester(a.view, 960, 640)
	clickInterface(t, tt, "添加设备")
	if a.engine.PairingUntil().IsZero() || !tt.HasText("取消添加") {
		t.Fatal("adding did not open the pairing window")
	}
	clickInterface(t, tt, "取消添加")
	if !a.engine.PairingUntil().IsZero() || len(a.engine.Peers.List()) != 0 {
		t.Fatal("cancel should close pairing without trusting a device")
	}
}

func TestPairedDevicesRevealSearchOnlyWhenAdding(t *testing.T) {
	a := interfaceTestApp(t)
	if _, err := a.engine.Peers.Allow("paired", "fingerprint", "工作电脑", "windows", "192.168.1.20:47821"); err != nil {
		t.Fatal(err)
	}
	a.devices = a.engine.Peers.List()
	tt := ui.NewTester(a.view, 960, 640)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if !tt.HasText("工作电脑") || tt.HasText("附近的设备") || tt.HasText("对端地址") {
		t.Fatal("paired devices should take priority over search and address input")
	}
	clickInterface(t, tt, "通过地址连接")
	if !tt.HasText("对端地址") {
		t.Fatal("manual connection should remain available on demand")
	}
	clickInterface(t, tt, "添加设备")
	if !tt.HasText("附近的设备") || !tt.HasText("等待配对") {
		t.Fatal("adding should reveal discovery and the pairing countdown")
	}
	clickInterface(t, tt, "取消添加")
	if tt.HasText("附近的设备") {
		t.Fatal("canceling should return to the paired device list")
	}
	a.discoveryMessage = "搜索失败，请检查本地网络权限"
	tt.Frame()
	if !tt.HasText(a.discoveryMessage) {
		t.Fatal("discovery errors should remain visible outside the adding flow")
	}
}

func TestNetworkPermissionReminderAppearsWhenAdding(t *testing.T) {
	previous := currentNetworkGuideKind
	t.Cleanup(func() { currentNetworkGuideKind = previous })
	currentNetworkGuideKind = guideMacLocalNetwork
	a := interfaceTestApp(t)
	tt := ui.NewTester(a.view, 960, 640)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if tt.HasText("查看连接指引") {
		t.Fatal("permission instructions should wait until the user adds a device")
	}
	clickInterface(t, tt, "添加设备")
	if !tt.HasText("查看连接指引") || !tt.HasText("本地网络权限") {
		t.Fatal("adding should explain the network permission before pairing")
	}
	clickInterface(t, tt, "取消添加")
	if tt.HasText("查看连接指引") || !tt.HasText("连接帮助") {
		t.Fatal("canceling should hide the reminder and preserve manual help")
	}
}

func TestRecoverHistoryKeepsDamagedFiles(t *testing.T) {
	a := interfaceTestApp(t)
	dir := a.hist.Dir()
	marker := filepath.Join(dir, "keep-me.txt")
	if err := os.WriteFile(marker, []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.hist = history.Unavailable(dir, os.ErrInvalid)
	a.recoverHistory()
	if a.hist.Disabled() != nil || !strings.Contains(a.status, "damaged") {
		t.Fatalf("status=%q disabled=%v", a.status, a.hist.Disabled())
	}
	matches, err := filepath.Glob(dir + ".damaged-*/keep-me.txt")
	if err != nil || len(matches) != 1 {
		t.Fatal(matches, err)
	}
}

func TestSettingsSaveFailureRollsBack(t *testing.T) {
	a := interfaceTestApp(t)
	parent := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Path = filepath.Join(parent, "settings.json")
	a.engine = nil
	a.paused = true
	a.textOnly = true
	a.maxImageText = "8"
	a.saveSettings()
	if a.paused || a.textOnly || a.maxImageText != "20" || a.cfg.Values.Paused || a.cfg.Values.TextOnly || a.cfg.Values.MaxImageMB != 20 {
		t.Fatalf("failed save changed visible settings: paused=%v textOnly=%v image=%s cfg=%+v", a.paused, a.textOnly, a.maxImageText, a.cfg.Values)
	}
	if a.status == "" || a.status == "已暂停同步" {
		t.Fatalf("status = %q", a.status)
	}
}

// Files once shared the text icon, which made the two rows look alike.
func TestHistoryRowsSeparateTextFilesAndFolders(t *testing.T) {
	text := historyKindIcon(history.Entry{Kind: string(clip.KindText)})
	file := historyKindIcon(history.Entry{Kind: string(clip.KindFiles), FileName: "报告.pdf", Folder: false})
	folder := historyKindIcon(history.Entry{Kind: string(clip.KindFiles), FileName: "资料", Folder: true})
	image := historyKindIcon(history.Entry{Kind: string(clip.KindImage)})
	if text == nil || file == nil || folder == nil || image == nil {
		t.Fatal("missing row icon")
	}
	if file == text {
		t.Error("single file uses the text icon")
	}
	if file == folder || folder == text {
		t.Error("folder, file and text icons must differ")
	}
	if image == text || image == file || image == folder {
		t.Error("image icon must stay unique")
	}
}

// The segmented order is 全部, 置顶, 文本, 图片, 文件.
func TestHistoryKindsFollowSegmentOrder(t *testing.T) {
	pinnedText := history.Entry{Kind: string(clip.KindText), Pinned: true}
	for _, tc := range []struct {
		kind  int
		entry history.Entry
		want  bool
	}{
		{0, pinnedText, true},
		{0, history.Entry{Kind: string(clip.KindFiles)}, true},
		{1, pinnedText, true},
		{1, history.Entry{Kind: string(clip.KindText)}, false},
		{2, history.Entry{Kind: string(clip.KindText)}, true},
		{2, history.Entry{Kind: string(clip.KindImage)}, false},
		{3, history.Entry{Kind: string(clip.KindImage)}, true},
		{3, history.Entry{Kind: string(clip.KindFiles)}, false},
		{4, history.Entry{Kind: string(clip.KindFiles)}, true},
		{4, history.Entry{Kind: string(clip.KindText)}, false},
	} {
		if got := historyKindMatches(tc.kind, tc.entry); got != tc.want {
			t.Errorf("kind %d with %q (pinned=%v) = %v, want %v", tc.kind, tc.entry.Kind, tc.entry.Pinned, got, tc.want)
		}
	}
}

func TestPickerResizeDragAppliesAndPersistsSize(t *testing.T) {
	a := interfaceTestApp(t)
	a.pickSizeW, a.pickSizeH = 380, 440
	a.pickDragW, a.pickDragH = 380, 440
	tt := ui.NewTester(a.pickerView, 380, 440)

	// The right edge resizes the width, the bottom edge the height.
	tt.Press(377, 200)
	tt.Move(477, 200)
	tt.Release(477, 200)
	if a.pickSizeW != 480 || a.pickSizeH != 440 {
		t.Fatalf("width drag gave %d×%d, want 480×440", a.pickSizeW, a.pickSizeH)
	}
	if a.cfg.Values.PickerWidth != 480 || a.cfg.Values.PickerHeight != 440 {
		t.Fatalf("size not remembered: %+v", a.cfg.Values)
	}

	tt.SetSize(480, 440)
	tt.Press(200, 437)
	tt.Move(200, 537)
	tt.Release(200, 537)
	if a.pickSizeW != 480 || a.pickSizeH != 540 {
		t.Fatalf("height drag gave %d×%d, want 480×540", a.pickSizeW, a.pickSizeH)
	}
	if a.cfg.Values.PickerHeight != 540 {
		t.Fatalf("height not remembered: %+v", a.cfg.Values)
	}
}

func TestPickerResizeStopsAtLengthBounds(t *testing.T) {
	a := interfaceTestApp(t)
	a.pickSizeW, a.pickSizeH = 380, 440
	a.pickDragW, a.pickDragH = 380, 440
	tt := ui.NewTester(a.pickerView, 380, 440)

	// Dragging past the minimum keeps the window usable, and dragging past
	// the maximum keeps it on screen.
	tt.Press(377, 200)
	tt.Move(-500, 200)
	tt.Release(-500, 200)
	if a.pickSizeW != settings.MinPickerWidth || a.pickSizeH != 440 {
		t.Fatalf("minimum not applied: %d×%d", a.pickSizeW, a.pickSizeH)
	}

	a.pickSizeW, a.pickSizeH, a.pickArea = 380, 440, mygo.Rectangle{Width: 700, Height: 600}
	tt.SetSize(380, 440)
	tt.Press(377, 437)
	tt.Move(2000, 2000)
	tt.Release(2000, 2000)
	if a.pickSizeW != 700 || a.pickSizeH != 600 {
		t.Fatalf("work area not applied: %d×%d, want 700×600", a.pickSizeW, a.pickSizeH)
	}
}

// The window has no title bar, so its padding moves it; a row keeps its own
// press.
func TestPickerPaddingMovesTheWindow(t *testing.T) {
	a := interfaceTestApp(t)
	item, err := clip.FromText("可拖动的快速历史")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.hist.Add(history.DirLocal, "", item); err != nil {
		t.Fatal(err)
	}
	a.refreshEntries()
	tt := ui.NewTester(a.pickerView, 380, 440)
	tt.Move(5, 220)
	if got := tt.Cursor(); got != ui.CursorGrab {
		t.Fatalf("padding cursor = %v, want the grab cursor", got)
	}
	row, ok := tt.Find("可拖动的快速历史")
	if !ok {
		t.Fatal("row not found")
	}
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	if got := tt.Cursor(); got != ui.CursorDefault {
		t.Fatalf("row cursor = %v, want the default cursor", got)
	}
}

func TestPickerPaddingDragMovesTheWindow(t *testing.T) {
	a := interfaceTestApp(t)
	a.pickSizeW, a.pickSizeH = 380, 440
	a.pickPlaced = true
	a.pickRect = mygo.Rectangle{X: 100, Y: 100, Width: 380, Height: 440}
	a.pickArea = mygo.Rectangle{X: 0, Y: 0, Width: 1440, Height: 900}
	// The window follows the pointer's place on the screen, so the test moves
	// that along with the pointer in the window.
	cursor := mygo.Point{X: 105, Y: 320}
	restore := pickerCursor
	pickerCursor = func() mygo.Point { return cursor }
	t.Cleanup(func() { pickerCursor = restore })
	tt := ui.NewTester(a.pickerView, 380, 440)

	tt.Press(5, 220)
	cursor = mygo.Point{X: 165, Y: 360}
	tt.Move(65, 260)
	tt.Release(65, 260)
	if a.pickRect.X != 160 || a.pickRect.Y != 140 || a.pickSizeW != 380 || a.pickSizeH != 440 {
		t.Fatalf("drag gave %+v, want 160,140 at 380x440", a.pickRect)
	}

	// The window stays on the screen it is dragged across.
	a.pickArea = mygo.Rectangle{X: 0, Y: 0, Width: 500, Height: 500}
	tt.Press(5, 220)
	cursor = mygo.Point{X: 705, Y: 780}
	tt.Move(600, 600)
	tt.Release(605, 460)
	if a.pickRect.X != 120 || a.pickRect.Y != 60 {
		t.Fatalf("drag past the work area gave %+v, want 120,60", a.pickRect)
	}
}

// A window that follows the pointer must follow its place on the screen, not
// the movements of a press: a press measured in a window that moved already
// counted them, and adding them up shakes the window.
func TestPickerDragFollowsThePointer(t *testing.T) {
	a := interfaceTestApp(t)
	a.pickSizeW, a.pickSizeH = 380, 440
	a.pickPlaced = true
	a.pickRect = mygo.Rectangle{X: 100, Y: 100, Width: 380, Height: 440}
	a.pickArea = mygo.Rectangle{X: 0, Y: 0, Width: 1440, Height: 900}
	cursor := mygo.Point{X: 105, Y: 320}
	restore := pickerCursor
	pickerCursor = func() mygo.Point { return cursor }
	t.Cleanup(func() { pickerCursor = restore })
	tt := ui.NewTester(a.pickerView, 380, 440)

	// Each frame of the drag moves the pointer and the window by the same
	// amount, so the window ends up exactly where the pointer went: no more
	// and no less.
	tt.Press(5, 220)
	for i := 1; i <= 20; i++ {
		cursor = mygo.Point{X: 105 + i*3, Y: 320 + i}
		tt.Move(float32(5+i*3), float32(220+i))
	}
	tt.Release(65, 240)
	if a.pickRect.X != 160 || a.pickRect.Y != 120 {
		t.Fatalf("drag of 60,20 gave %+v, want 160,120", a.pickRect)
	}
}

// The sidebar header carries the bird the app icon shows: decoded on the first
// frame, then kept, so no frame pays for it again.
func TestSidebarHeaderShowsTheLogoOnce(t *testing.T) {
	a := interfaceTestApp(t)
	a.page = pageDevices
	tt := ui.NewTester(a.view, 960, 640)
	logo := a.logo
	if logo == nil {
		t.Fatal("sidebar header did not load the logo")
	}
	tt.Frame()
	if a.logo != logo {
		t.Fatal("the logo was decoded again for the next frame")
	}
	if !tt.HasText("Snapover") {
		t.Fatal("the wordmark left the header the logo joined")
	}
}
