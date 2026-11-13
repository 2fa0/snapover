//go:build darwin

package front

import (
	"strings"
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

// CurrentSource is the app in front.
// It does not need Accessibility permission. Zero means unknown.
func CurrentSource() Source {
	ws := objc.ID(objc.GetClass("NSWorkspace")).Send(selSharedWorkspace)
	if ws == 0 {
		return Source{}
	}
	return describe(ws.Send(selFrontmost))
}

// describe names a running application, preferring its localized name and
// falling back to the identifier when it has none.
func describe(app objc.ID) Source {
	if app == 0 {
		return Source{}
	}
	s := Source{Name: nsString(app.Send(selLocalizedName)), ID: nsString(app.Send(selBundleID))}
	if s.Name == "" {
		s.Name = s.ID
	}
	return s
}

// nsString copies what a string answers with: the buffer belongs to the
// autorelease pool it was made in, so a string that aliased it would change
// with whatever that memory is used for next.
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
	return strings.Clone(unsafe.String((*byte)(ptr), n))
}
