//go:build darwin

package paste

import (
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	kCGHIDEventTap          = 0
	kCGEventFlagMaskCommand = 0x00100000
	kVK_ANSI_V              = 0x09
	activateIgnoringOthers  = 2
)

var (
	selSharedWorkspace = objc.RegisterName("sharedWorkspace")
	selFrontmost       = objc.RegisterName("frontmostApplication")
	selProcessID       = objc.RegisterName("processIdentifier")
	selRunningApp      = objc.RegisterName("runningApplicationWithProcessIdentifier:")
	selActivate        = objc.RegisterName("activateWithOptions:")
	selNumberWithBool  = objc.RegisterName("numberWithBool:")
	selDict            = objc.RegisterName("dictionaryWithObject:forKey:")
	selUTF8            = objc.RegisterName("stringWithUTF8String:")

	axOnce              sync.Once
	axTrusted           func(opts uintptr) uint8
	axErr               error
	axCreateApp         func(pid int32) uintptr
	axCopyAttribute     func(element, attribute uintptr, value *uintptr) int32
	axCopyParameterized func(element, attribute, parameter uintptr, value *uintptr) int32
	axGetValue          func(value uintptr, kind uint32, out unsafe.Pointer) uint8
	axCreateValue       func(kind uint32, value unsafe.Pointer) uintptr
	axSetTimeout        func(element uintptr, seconds float32) int32
	axRelease           func(ref uintptr)

	cgOnce      sync.Once
	cgCreate    func(source uintptr, key uint16, down uint8) uintptr
	cgFlags     func(event uintptr, flags uint64)
	cgPost      func(tap uint32, event uintptr)
	cfRelease   func(ref uintptr)
	cgErr       error
	cgModifiers func(state uint32) uint64

	prompted bool
)

// Capture returns the frontmost app, unless it is this process.
// Call it on the main thread.
func Capture() Target {
	ws := objc.ID(objc.GetClass("NSWorkspace")).Send(selSharedWorkspace)
	if ws == 0 {
		return Target{}
	}
	app := ws.Send(selFrontmost)
	if app == 0 {
		return Target{}
	}
	pid := int(objc.Send[int32](app, selProcessID))
	if pid == 0 || pid == os.Getpid() {
		return Target{}
	}
	return Target{id: uintptr(pid)}
}

// Allowed reports whether this process may type into other apps.
func Allowed() bool {
	loadAX()
	if axTrusted == nil {
		return false
	}
	return axTrusted(0) != 0
}

// Prompt asks macOS to show the Accessibility permission once.
func Prompt() {
	if prompted || Allowed() {
		return
	}
	prompted = true
	loadAX()
	if axTrusted == nil {
		return
	}
	key := nsString("AXTrustedCheckOptionPrompt")
	yes := objc.ID(objc.GetClass("NSNumber")).Send(selNumberWithBool, uint8(1))
	dict := objc.ID(objc.GetClass("NSDictionary")).Send(selDict, yes, key)
	_ = axTrusted(uintptr(dict))
}

// Activate restores the target; the caller schedules the focus delay off-thread.
// Call it on the main thread.
func (t Target) Activate() error {
	if !t.Valid() {
		return ErrNoTarget
	}
	if !Allowed() {
		Prompt()
		return ErrNeedAccess
	}
	app := objc.ID(objc.GetClass("NSRunningApplication")).Send(selRunningApp, int32(t.id))
	if app == 0 {
		return ErrNoTarget
	}
	_ = objc.Send[bool](app, selActivate, uintptr(activateIgnoringOthers))
	return nil
}

func (t Target) SendPaste() error {
	if !t.Valid() || Capture() != t {
		return ErrActivate
	}
	if !Allowed() {
		return ErrNeedAccess
	}
	return postCommandV()
}

func postCommandV() error { return postCommandKey(kVK_ANSI_V) }

func postCommandKey(key uint16) error {
	loadCG()
	if cgErr != nil {
		return cgErr
	}
	down := cgCreate(0, key, 1)
	up := cgCreate(0, key, 0)
	if down == 0 || up == 0 {
		if down != 0 {
			cfRelease(down)
		}
		if up != 0 {
			cfRelease(up)
		}
		return ErrActivate
	}
	cgFlags(down, kCGEventFlagMaskCommand)
	cgFlags(up, kCGEventFlagMaskCommand)
	cgPost(kCGHIDEventTap, down)
	cgPost(kCGHIDEventTap, up)
	cfRelease(down)
	cfRelease(up)
	return nil
}

func loadAX() {
	axOnce.Do(func() {
		h, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			axErr = err
			return
		}
		purego.RegisterLibFunc(&axTrusted, h, "AXIsProcessTrustedWithOptions")
		purego.RegisterLibFunc(&axCreateApp, h, "AXUIElementCreateApplication")
		purego.RegisterLibFunc(&axCopyAttribute, h, "AXUIElementCopyAttributeValue")
		purego.RegisterLibFunc(&axCopyParameterized, h, "AXUIElementCopyParameterizedAttributeValue")
		purego.RegisterLibFunc(&axGetValue, h, "AXValueGetValue")
		purego.RegisterLibFunc(&axCreateValue, h, "AXValueCreate")
		purego.RegisterLibFunc(&axSetTimeout, h, "AXUIElementSetMessagingTimeout")
		purego.RegisterLibFunc(&axRelease, h, "CFRelease")
	})
}

func loadCG() {
	cgOnce.Do(func() {
		h, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			cgErr = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			cgErr = err
			return
		}
		purego.RegisterLibFunc(&cgCreate, h, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgFlags, h, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgPost, h, "CGEventPost")
		purego.RegisterLibFunc(&cgModifiers, h, "CGEventSourceFlagsState")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	})
}

func nsString(s string) objc.ID {
	buf := append([]byte(s), 0)
	return objc.ID(objc.GetClass("NSString")).Send(selUTF8, unsafe.Pointer(&buf[0]))
}

// ModifiersDown lets the copy shortcut wait for physical keys to be released.
func ModifiersDown() bool {
	loadCG()
	return cgErr == nil && cgModifiers(1)&0x1e0000 != 0
}

func (t Target) SendCopy() error {
	if !t.Valid() || Capture() != t {
		return ErrActivate
	}
	if !Allowed() {
		Prompt()
		return ErrNeedAccess
	}
	return postCommandKey(0x08) // kVK_ANSI_C
}
