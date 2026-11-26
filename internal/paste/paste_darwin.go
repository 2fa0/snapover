//go:build darwin

package paste

import (
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	kCGHIDEventTap          = 0
	kCGEventFlagMaskCommand = 0x00100000
	kVK_ANSI_V              = 0x09
	activateIgnoringOthers  = 2
	axErrNoValue            = -25212
)

var (
	selSharedWorkspace  = objc.RegisterName("sharedWorkspace")
	selFrontmost        = objc.RegisterName("frontmostApplication")
	selProcessID        = objc.RegisterName("processIdentifier")
	selRunningApp       = objc.RegisterName("runningApplicationWithProcessIdentifier:")
	selActivate         = objc.RegisterName("activateWithOptions:")
	selNumberWithBool   = objc.RegisterName("numberWithBool:")
	selDict             = objc.RegisterName("dictionaryWithObject:forKey:")
	selUTF8             = objc.RegisterName("stringWithUTF8String:")
	selBundleIdentifier = objc.RegisterName("bundleIdentifier")
	selUTF8String       = objc.RegisterName("UTF8String")

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

	cfOnce             sync.Once
	cfGetTypeID        func(cf uintptr) uintptr
	cfStringGetTypeID  func() uintptr
	cfStringGetCString func(str uintptr, buf *byte, maxSize int64, encoding uint32) uint8

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

func loadCF() {
	cfOnce.Do(func() {
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&cfGetTypeID, cf, "CFGetTypeID")
		purego.RegisterLibFunc(&cfStringGetTypeID, cf, "CFStringGetTypeID")
		purego.RegisterLibFunc(&cfStringGetCString, cf, "CFStringGetCString")
	})
}

func cfString(ref uintptr) (string, bool) {
	if ref == 0 || cfGetTypeID == nil || cfStringGetTypeID == nil || cfStringGetCString == nil {
		return "", false
	}
	if cfGetTypeID(ref) != cfStringGetTypeID() {
		return "", false
	}
	var buf [128]byte
	if cfStringGetCString(ref, &buf[0], int64(len(buf)), 0x08000100) == 0 {
		return "", false
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i]), true
		}
	}
	return string(buf[:]), true
}

func objcGoString(ptr unsafe.Pointer, limit int) string {
	var n int
	for n < limit && *(*byte)(unsafe.Add(ptr, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(ptr), n))
}

func isFinderTarget(pid int32) bool {
	if pid == 0 {
		return false
	}
	app := objc.ID(objc.GetClass("NSRunningApplication")).Send(selRunningApp, pid)
	if app == 0 {
		return false
	}
	bid := app.Send(selBundleIdentifier)
	if bid == 0 {
		return false
	}
	ptr := objc.Send[unsafe.Pointer](bid, selUTF8String)
	if ptr == nil {
		return false
	}
	return objcGoString(ptr, 64) == "com.apple.finder"
}

func isTextInputRole(role string) bool {
	switch role {
	case "AXTextField", "AXTextArea", "AXComboBox", "AXSearchField":
		return true
	default:
		return false
	}
}

func hasFocusedTextInput(pid int32) (bool, error) {
	loadCF()
	deadline := time.Now().Add(caretTimeout)
	app := axCreateApp(pid)
	if app == 0 {
		return false, ErrUnknownFocus
	}
	defer axRelease(app)
	focusedAttribute := nsString("AXFocusedUIElement")
	if !setAXTimeout(app, deadline) {
		return false, ErrUnknownFocus
	}
	var element uintptr
	switch axCopyAttribute(app, uintptr(focusedAttribute), &element) {
	case axErrNoValue:
		return false, nil
	case 0:
		if element == 0 {
			return false, ErrUnknownFocus
		}
	default:
		return false, ErrUnknownFocus
	}
	defer axRelease(element)
	roleAttribute := nsString("AXRole")
	if !setAXTimeout(element, deadline) {
		return false, ErrUnknownFocus
	}
	var roleRef uintptr
	if axCopyAttribute(element, uintptr(roleAttribute), &roleRef) != 0 || roleRef == 0 {
		return false, ErrUnknownFocus
	}
	defer axRelease(roleRef)
	role, ok := cfString(roleRef)
	if !ok || role == "" {
		return false, ErrUnknownFocus
	}
	return isTextInputRole(role), nil
}

// CheckPasteTarget prevents automatic text and image pastes into Finder's file area.
// Call on the main thread after attempting to restore focus.
func (t Target) CheckPasteTarget(isFiles bool) error {
	pid := int32(t.id)
	if isFiles || !isFinderTarget(pid) {
		return nil
	}
	if !t.Valid() || Capture() != t {
		return ErrActivate
	}
	if !Allowed() {
		return ErrNeedAccess
	}
	textInput, err := hasFocusedTextInput(pid)
	if err != nil {
		return err
	}
	if !textInput {
		return ErrNonTextTarget
	}
	return nil
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
