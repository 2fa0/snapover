//go:build darwin

package apps

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"math"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/image/draw"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var (
	selNew             = objc.RegisterName("new")
	selDrain           = objc.RegisterName("drain")
	selSharedWorkspace = objc.RegisterName("sharedWorkspace")
	selRunningApps     = objc.RegisterName("runningApplications")
	selCount           = objc.RegisterName("count")
	selObjectAtIndex   = objc.RegisterName("objectAtIndex:")
	selPolicy          = objc.RegisterName("activationPolicy")
	selLocalizedName   = objc.RegisterName("localizedName")
	selBundleID        = objc.RegisterName("bundleIdentifier")
	selMainBundle      = objc.RegisterName("mainBundle")
	selIcon            = objc.RegisterName("icon")
	selIconForFile     = objc.RegisterName("iconForFile:")
	selBundleWithPath  = objc.RegisterName("bundleWithPath:")
	selInfoDictionary  = objc.RegisterName("infoDictionary")
	selObjectForKey    = objc.RegisterName("objectForKey:")
	selTIFF            = objc.RegisterName("TIFFRepresentation")
	selImageRepWith    = objc.RegisterName("imageRepWithData:")
	selRepresentation  = objc.RegisterName("representationUsingType:properties:")
	selDictionary      = objc.RegisterName("dictionary")
	selBytes           = objc.RegisterName("bytes")
	selLength          = objc.RegisterName("length")
	selUTF8String      = objc.RegisterName("UTF8String")
	selStringWithUTF8  = objc.RegisterName("stringWithUTF8String:")
	selRepresentations = objc.RegisterName("representations")
	selPixelsWide      = objc.RegisterName("pixelsWide")
	selRespondsTo      = objc.RegisterName("respondsToSelector:")
)

// activationRegular is NSApplicationActivationPolicyRegular: an app with a
// Dock tile and windows. The other policies are background agents and
// clients of another app, which a user cannot copy from by choice.
const activationRegular = 0

// pngFileType is NSBitmapImageFileTypePNG.
const pngFileType = 4

// worker runs every AppKit message on one thread of its own, inside the
// autorelease pool the objects it makes there expect, as internal/clip does
// for the pasteboard. App icons are read from other threads without a pool.
var worker = struct {
	once sync.Once
	jobs chan func()
	err  error
}{}

func onWorker(job func()) {
	worker.once.Do(func() {
		worker.jobs = make(chan func())
		go func() {
			runtime.LockOSThread()
			// AppKit answers for the apps of this Mac: a binary that never
			// drew a window, as tests do not, has to load it first.
			if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL); err != nil {
				worker.err = err
			}
			for job := range worker.jobs {
				pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(selNew)
				job()
				pool.Send(selDrain)
			}
		}()
	})
	done := make(chan struct{})
	worker.jobs <- func() {
		defer close(done)
		job()
	}
	<-done
}

// withPool drains what AppKit autoreleased for one app before the next one:
// the TIFF an app icon is converted from is tens of megabytes, and a list of
// running apps holds one at a time.
func withPool(job func()) {
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(selNew)
	defer pool.Send(selDrain)
	job()
}

// icons keeps what was converted already: a list is redrawn on every update,
// and the conversion reads a megapixel of app icon each time.
var icons = struct {
	sync.Mutex
	byApp map[string][]byte
}{byApp: make(map[string][]byte)}

func running() []App {
	var list []App
	onWorker(func() {
		workspace := objc.ID(objc.GetClass("NSWorkspace")).Send(selSharedWorkspace)
		if workspace == 0 {
			return
		}
		runningApps := workspace.Send(selRunningApps)
		if runningApps == 0 {
			return
		}
		self := nsString(objc.ID(objc.GetClass("NSBundle")).Send(selMainBundle).Send(selBundleID))
		count := objc.Send[uint64](runningApps, selCount)
		seen := make(map[string]bool, count)
		for i := uint64(0); i < count; i++ {
			app := objc.Send[objc.ID](runningApps, selObjectAtIndex, i)
			if app == 0 || objc.Send[int64](app, selPolicy) != activationRegular {
				continue
			}
			entry := App{Name: nsString(app.Send(selLocalizedName)), ID: nsString(app.Send(selBundleID))}
			if entry.Name == "" {
				entry.Name = entry.ID
			}
			key := entry.ID
			if key == "" {
				key = entry.Name
			}
			if key == "" || key == self || seen[key] {
				continue
			}
			seen[key] = true
			withPool(func() { entry.Icon = icon(key, app.Send(selIcon)) })
			list = append(list, entry)
		}
	})
	return list
}

func describe(path string) (App, error) {
	var entry App
	var err error
	onWorker(func() {
		bundle := objc.ID(objc.GetClass("NSBundle")).Send(selBundleWithPath, nsStringWith(path))
		if bundle == 0 {
			err = errors.New("不是应用程序")
			return
		}
		entry = App{ID: nsString(bundle.Send(selBundleID))}
		entry.Name = bundleName(bundle)
		if entry.Name == "" {
			entry.Name = strings.TrimSuffix(filepath.Base(strings.TrimSuffix(path, "/")), ".app")
		}
		workspace := objc.ID(objc.GetClass("NSWorkspace")).Send(selSharedWorkspace)
		if workspace != 0 {
			key := entry.ID
			if key == "" {
				key = entry.Name
			}
			withPool(func() { entry.Icon = icon(key, workspace.Send(selIconForFile, nsStringWith(path))) })
		}
	})
	if err == nil && entry.Name == "" {
		err = errors.New("不是应用程序")
	}
	return entry, err
}

// bundleName is the name the app shows, which is its display name rather
// than the file name it was installed under.
func bundleName(bundle objc.ID) string {
	info := bundle.Send(selInfoDictionary)
	if info == 0 {
		return ""
	}
	for _, key := range []string{"CFBundleDisplayName", "CFBundleName"} {
		if name := nsString(info.Send(selObjectForKey, nsStringWith(key))); name != "" {
			return name
		}
	}
	return ""
}

// icon converts the icon of one app once, and remembers it: the settings
// list shows the same app on every update.
func icon(key string, image objc.ID) []byte {
	icons.Lock()
	cached := icons.byApp[key]
	icons.Unlock()
	if cached != nil {
		return cached
	}
	data := pngOf(image)
	if data == nil {
		return nil
	}
	icons.Lock()
	icons.byApp[key] = data
	icons.Unlock()
	return data
}

// pngOf draws an NSImage into PNG pixels, scaled down to what the list shows.
// An app icon carries representations up to 1024 pixels wide, and converting
// all of them takes a sixth of a second for an image drawn at 20 points, so
// the smallest representation of at least that size is the one converted.
func pngOf(image objc.ID) []byte {
	if image == 0 {
		return nil
	}
	properties := objc.ID(objc.GetClass("NSDictionary")).Send(selDictionary)
	data := pngData(iconRep(image), properties)
	if data == 0 {
		rep := objc.ID(objc.GetClass("NSBitmapImageRep")).Send(selImageRepWith, image.Send(selTIFF))
		data = pngData(rep, properties)
	}
	if data == 0 {
		return nil
	}
	bytesPtr := objc.Send[unsafe.Pointer](data, selBytes)
	size := objc.Send[uint64](data, selLength)
	if bytesPtr == nil || size == 0 {
		return nil
	}
	return smallPNG(unsafe.Slice((*byte)(bytesPtr), size))
}

// pngData is PNG data of a representation, or 0 when it cannot make one:
// the icons of system apps are representations of their own kind.
func pngData(rep objc.ID, properties objc.ID) objc.ID {
	if rep == 0 || !objc.Send[bool](rep, selRespondsTo, selRepresentation) {
		return 0
	}
	return rep.Send(selRepresentation, uint64(pngFileType), properties)
}

// iconRep is the smallest representation of the image that still has the
// pixels the list shows, or the largest one there is.
func iconRep(image objc.ID) objc.ID {
	representations := image.Send(selRepresentations)
	if representations == 0 {
		return 0
	}
	count := objc.Send[uint64](representations, selCount)
	var best objc.ID
	bestWidth := 0
	for i := uint64(0); i < count; i++ {
		rep := objc.Send[objc.ID](representations, selObjectAtIndex, i)
		width := int(objc.Send[int64](rep, selPixelsWide))
		if width < 1 {
			continue
		}
		if best == 0 || width >= iconPixels && (bestWidth < iconPixels || width < bestWidth) || bestWidth < iconPixels && width > bestWidth {
			best, bestWidth = rep, width
		}
	}
	return best
}

// smallPNG keeps an icon sharp at iconPixels and no larger.
func smallPNG(data []byte) []byte {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	bounds := img.Bounds()
	if bounds.Dx() < 1 || bounds.Dy() < 1 {
		return nil
	}
	scale := math.Min(1, float64(iconPixels)/float64(max(bounds.Dx(), bounds.Dy())))
	w := max(1, int(math.Round(float64(bounds.Dx())*scale)))
	h := max(1, int(math.Round(float64(bounds.Dy())*scale)))
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Src, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil
	}
	return out.Bytes()
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

// nsStringWith makes an NSString of s, which the runtime copies.
func nsStringWith(s string) objc.ID {
	return objc.ID(objc.GetClass("NSString")).Send(selStringWithUTF8, unsafe.StringData(s+"\x00"))
}
