//go:build darwin

package clip

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"golang.org/x/image/tiff"
)

// Exercise native data access using an isolated named pasteboard. The user's
// general clipboard, application focus and active windows are untouched.
func privateReadPasteboard(t *testing.T) objc.ID {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL); err != nil {
		t.Fatal(err)
	}
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	t.Cleanup(func() { pool.Send(objc.RegisterName("drain")) })
	pb := objc.ID(objc.GetClass("NSPasteboard")).Send(objc.RegisterName("pasteboardWithUniqueName"))
	if pb == 0 {
		t.Fatal("private pasteboard unavailable")
	}
	t.Cleanup(func() { pb.Send(objc.RegisterName("releaseGlobally")) })
	oldName, oldRun := readPasteboardName, runOnClipboardThread
	name := pb.Send(objc.RegisterName("name"))
	callerThread := objc.ID(objc.GetClass("NSThread")).Send(objc.RegisterName("currentThread"))
	readPasteboardName = func() objc.ID {
		thread := objc.ID(objc.GetClass("NSThread")).Send(objc.RegisterName("currentThread"))
		if thread == callerThread || objc.Send[bool](thread, objc.RegisterName("isMainThread")) {
			t.Error("provider data read did not move to a background thread")
		}
		return name
	}
	runOnClipboardThread = func(fn func()) { t.Error("native data read dispatched to the UI thread"); fn() }
	t.Cleanup(func() { readPasteboardName, runOnClipboardThread = oldName, oldRun })
	return pb
}

func TestNativeImageReadOnPrivatePasteboard(t *testing.T) {
	pb := privateReadPasteboard(t)

	img := image.NewNRGBA(image.Rect(0, 0, 11, 13))
	img.SetNRGBA(3, 5, color.NRGBA{R: 33, G: 77, B: 99, A: 255})
	for _, format := range []string{"public.png", "public.tiff"} {
		var raw bytes.Buffer
		if format == "public.png" {
			if err := png.Encode(&raw, img); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := tiff.Encode(&raw, img, nil); err != nil {
				t.Fatal(err)
			}
		}
		data := objc.ID(objc.GetClass("NSData")).Send(objc.RegisterName("dataWithBytes:length:"), unsafe.Pointer(&raw.Bytes()[0]), uint64(raw.Len()))
		typ := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), unsafe.StringData(format+"\x00"))
		pb.Send(objc.RegisterName("clearContents"))
		if !objc.Send[bool](pb, objc.RegisterName("setData:forType:"), data, typ) {
			t.Fatal("private write failed")
		}
		item, err := readClipboardImage()
		if err != nil || item.Hash != HashImage(img) || item.Width != 11 || item.Height != 13 {
			t.Fatalf("%s: native pixels changed: %v", format, err)
		}
	}
}

func TestNativeTextReadLimitsOnPrivatePasteboard(t *testing.T) {
	pb := privateReadPasteboard(t)
	text := "你好\n clipboard"
	data := objc.ID(objc.GetClass("NSData")).Send(objc.RegisterName("dataWithBytes:length:"), unsafe.StringData(text), uint64(len(text)))
	typ := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), unsafe.StringData("public.utf8-plain-text\x00"))
	if !objc.Send[bool](pb, objc.RegisterName("setData:forType:"), data, typ) {
		t.Fatal("private write failed")
	}
	if got, err := readLimitedClipboardText(len(text)); err != nil || got != text {
		t.Fatalf("native text changed: %q %v", got, err)
	}
	if got, err := readLimitedClipboardText(len(text) - 1); err == nil || got != "" {
		t.Fatalf("over-limit text copied: %q %v", got, err)
	}
	if raw, err := readNativePasteboardData("public.utf8-plain-text", 1); err == nil || len(raw) != 0 {
		t.Fatal("over-limit native bytes copied into Go")
	}
	pb.Send(objc.RegisterName("clearContents"))
	if got, err := readLimitedClipboardText(100); err != nil || got != "" {
		t.Fatalf("empty pasteboard: %q %v", got, err)
	}
}
