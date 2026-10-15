//go:build darwin

package sendto

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

func TestNativeServicePreservesPathsAndDoesNotUseGeneralClipboard(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL); err != nil {
		t.Fatal(err)
	}
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	general := objc.ID(objc.GetClass("NSPasteboard")).Send(objc.RegisterName("generalPasteboard"))
	sequence := objc.Send[int64](general, objc.RegisterName("changeCount"))
	board := objc.ID(objc.GetClass("NSPasteboard")).Send(objc.RegisterName("pasteboardWithUniqueName"))
	defer board.Send(objc.RegisterName("releaseGlobally"))
	want := []string{
		filepath.Join(t.TempDir(), "中文 报告.pdf"),
		filepath.Join(t.TempDir(), "line\nbreak ' `touch injected` $HOME.txt"),
	}
	urls := objc.ID(objc.GetClass("NSMutableArray")).Send(objc.RegisterName("array"))
	for _, path := range want {
		url := objc.ID(objc.GetClass("NSURL")).Send(objc.RegisterName("fileURLWithPath:"), nsString(path))
		urls.Send(objc.RegisterName("addObject:"), url)
	}
	if !objc.Send[bool](board, objc.RegisterName("writeObjects:"), urls) {
		t.Fatal("could not prepare private service pasteboard")
	}
	var got []string
	if err := Register(func(paths []string) error {
		got = paths
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { serviceHandler = nil }()
	app := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
	provider := app.Send(objc.RegisterName("servicesProvider"))
	var errorOut objc.ID
	provider.Send(objc.RegisterName("sendFiles:userData:error:"), board, objc.ID(0), &errorOut)
	if errorOut != 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("service paths = %q, error = %s", got, goString(errorOut))
	}
	// Re-registering replaces the handler without retaining another provider.
	denied := errors.New("发送文件服务已停用")
	if err := Register(func([]string) error { return denied }); err != nil {
		t.Fatal(err)
	}
	if app.Send(objc.RegisterName("servicesProvider")) != provider {
		t.Fatal("register retained a second service provider")
	}
	provider.Send(objc.RegisterName("sendFiles:userData:error:"), board, objc.ID(0), &errorOut)
	if goString(errorOut) != denied.Error() {
		t.Fatalf("service did not report refusal: %s", goString(errorOut))
	}
	board.Send(objc.RegisterName("clearContents"))
	errorOut = 0
	provider.Send(objc.RegisterName("sendFiles:userData:error:"), board, objc.ID(0), &errorOut)
	if errorOut == 0 {
		t.Fatal("empty selection was accepted")
	}
	if objc.Send[int64](general, objc.RegisterName("changeCount")) != sequence {
		t.Fatal("service modified the general clipboard")
	}
}

func TestNativeServiceRemovesOnlyLegacySnapoverWorkflow(t *testing.T) {
	services := t.TempDir()
	legacy := filepath.Join(services, "Snapover 发送文件.workflow", "Contents")
	other := filepath.Join(services, "Other.workflow")
	for _, dir := range []string{legacy, other} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeLegacyWorkflow(services); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy workflow still exists: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("unrelated workflow was removed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Install(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled service install: %v", err)
	}
}
