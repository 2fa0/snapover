//go:build darwin

package sendto

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var serviceHandler func([]string) error

var serviceClass = sync.OnceValues(func() (objc.Class, error) {
	return objc.RegisterClass("SnapoverFileService", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		{Cmd: objc.RegisterName("sendFiles:userData:error:"), Fn: receiveFiles},
	})
})

// Register connects Finder's service to the existing app. Call on the main
// thread, after the app is ready: macOS may deliver a request immediately.
func Register(handler func([]string) error) error {
	class, err := serviceClass()
	if err != nil {
		return err
	}
	serviceHandler = handler
	app := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
	provider := app.Send(objc.RegisterName("servicesProvider"))
	if provider == 0 || provider.Class() != class {
		provider = objc.ID(class).Send(objc.RegisterName("new"))
		app.Send(objc.RegisterName("setServicesProvider:"), provider)
		provider.Send(objc.RegisterName("release"))
	}
	return nil
}

func receiveFiles(_ objc.ID, _ objc.SEL, board objc.ID, _ objc.ID, errorOut *objc.ID) {
	paths := filePaths(board)
	var err error
	switch {
	case len(paths) == 0:
		err = errors.New("请在 Finder 中选择要发送的文件")
	case serviceHandler == nil:
		err = errors.New("请先启动 Snapover，再发送文件")
	default:
		err = serviceHandler(paths)
	}
	if err != nil && errorOut != nil {
		*errorOut = nsString(err.Error())
	}
}

// Services use a private pasteboard. Never read or modify the user's clipboard.
func filePaths(board objc.ID) []string {
	classes := objc.ID(objc.GetClass("NSArray")).Send(objc.RegisterName("arrayWithObject:"), objc.ID(objc.GetClass("NSURL")))
	yes := objc.ID(objc.GetClass("NSNumber")).Send(objc.RegisterName("numberWithBool:"), true)
	options := objc.ID(objc.GetClass("NSDictionary")).Send(objc.RegisterName("dictionaryWithObject:forKey:"), yes, nsString("NSPasteboardURLReadingFileURLsOnlyKey"))
	urls := board.Send(objc.RegisterName("readObjectsForClasses:options:"), classes, options)
	count := objc.Send[uint64](urls, objc.RegisterName("count"))
	paths := make([]string, 0, count)
	for i := uint64(0); i < count; i++ {
		url := urls.Send(objc.RegisterName("objectAtIndex:"), i)
		if !objc.Send[bool](url, objc.RegisterName("isFileURL")) {
			continue
		}
		if path := goString(url.Send(objc.RegisterName("path"))); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// WasOpenedForService must be checked during application startup while the
// launch Apple event is current, before registering the provider.
func WasOpenedForService() bool {
	event := objc.ID(objc.GetClass("NSAppleEventManager")).Send(objc.RegisterName("sharedAppleEventManager")).Send(objc.RegisterName("currentAppleEvent"))
	const keyAEPropData, keyAELaunchedAsServiceItem uint32 = 0x70726474, 0x73766974 // 'prdt', 'svit'
	prop := event.Send(objc.RegisterName("paramDescriptorForKeyword:"), keyAEPropData)
	return prop != 0 && objc.Send[uint32](prop, objc.RegisterName("enumCodeValue")) == keyAELaunchedAsServiceItem
}

func Install(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if filepath.Base(filepath.Dir(exe)) != "MacOS" || filepath.Base(filepath.Dir(filepath.Dir(exe))) != "Contents" || !strings.HasSuffix(filepath.Dir(filepath.Dir(filepath.Dir(exe))), ".app") {
		return errors.New("请将 Snapover.app 放到「应用程序」，启动后刷新 Finder 服务")
	}
	// The native service is declared in the signed app's Info.plist. Remove
	// only Snapover's previous Automator entry when migrating to it.
	return Remove()
}

func Remove() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return removeLegacyWorkflow(filepath.Join(home, "Library", "Services"))
}

func removeLegacyWorkflow(services string) error {
	return os.RemoveAll(filepath.Join(services, "Snapover 发送文件.workflow"))
}

var refreshServices = sync.OnceValues(func() (func(), error) {
	lib, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	address, err := purego.Dlsym(lib, "NSUpdateDynamicServices")
	if err != nil {
		return nil, err
	}
	var refresh func()
	purego.RegisterFunc(&refresh, address)
	return refresh, nil
})

// Refresh asks macOS to discover the app's native service. Call on the main thread.
func Refresh() error {
	refresh, err := refreshServices()
	if err == nil {
		refresh()
	}
	return err
}

func nsString(s string) objc.ID {
	buf := append([]byte(s), 0)
	return objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), unsafe.Pointer(&buf[0]))
}

func goString(s objc.ID) string {
	p := objc.Send[unsafe.Pointer](s, objc.RegisterName("UTF8String"))
	if p == nil {
		return ""
	}
	n := objc.Send[uint64](s, objc.RegisterName("lengthOfBytesUsingEncoding:"), uint64(4)) // NSUTF8StringEncoding
	return string(unsafe.Slice((*byte)(p), n))
}
