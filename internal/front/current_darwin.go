//go:build darwin

package front

import (
	"unsafe"

	"github.com/ebitengine/purego/objc"
)

var (
	selSharedWorkspace = objc.RegisterName("sharedWorkspace")
	selFrontmost       = objc.RegisterName("frontmostApplication")
	selLocalizedName   = objc.RegisterName("localizedName")
	selBundleID        = objc.RegisterName("bundleIdentifier")
	selUTF8String      = objc.RegisterName("UTF8String")
)

// Current is the localized name of the foreground app.
// It does not need Accessibility permission. An empty string means unknown.
func Current() string {
	ws := objc.ID(objc.GetClass("NSWorkspace")).Send(selSharedWorkspace)
	if ws == 0 {
		return ""
	}
	app := ws.Send(selFrontmost)
	if app == 0 {
		return ""
	}
	if name := nsString(app.Send(selLocalizedName)); name != "" {
		return name
	}
	return nsString(app.Send(selBundleID))
}

func nsString(id objc.ID) string {
	if id == 0 {
		return ""
	}
	ptr := objc.Send[unsafe.Pointer](id, selUTF8String)
	if ptr == nil {
		return ""
	}
	var n int
	for *(*byte)(unsafe.Add(ptr, n)) != 0 {
		n++
		if n == 512 {
			break
		}
	}
	return unsafe.String((*byte)(ptr), n)
}
