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
func TestNativeImageReadOnPrivatePasteboard(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL); err != nil {
		t.Fatal(err)
	}
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	pb := objc.ID(objc.GetClass("NSPasteboard")).Send(objc.RegisterName("pasteboardWithUniqueName"))
	if pb == 0 {
		t.Fatal("private pasteboard unavailable")
	}
	defer pb.Send(objc.RegisterName("releaseGlobally"))
	oldBoard, oldRun := imagePasteboard, runOnClipboardThread
	imagePasteboard = func() objc.ID { return pb }
	runOnClipboardThread = func(fn func()) { fn() }
	defer func() { imagePasteboard, runOnClipboardThread = oldBoard, oldRun }()
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
