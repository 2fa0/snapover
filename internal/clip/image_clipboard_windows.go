//go:build windows

package clip

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/2fa0/snapover/internal/crashlog"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"golang.org/x/sys/windows"
)

// Images are read and published natively instead of through MyGo's transfer
// layer for two reasons that the generic path cannot express: CF_DIB must be a
// 24-bit bottom-up bitmap for Feishu, and a synthesized CF_DIBV5 has to be
// rejected on read. MyGo enumerates clipboard formats rather than asking the
// system to synthesize them; the read path here follows the same rule.
var clipUser = windows.NewLazySystemDLL("user32.dll")
var clipKernel = windows.NewLazySystemDLL("kernel32.dll")
var (
	openClipboard     = clipUser.NewProc("OpenClipboard")
	closeClipboard    = clipUser.NewProc("CloseClipboard")
	emptyClipboard    = clipUser.NewProc("EmptyClipboard")
	getClipboardData  = clipUser.NewProc("GetClipboardData")
	setClipboardData  = clipUser.NewProc("SetClipboardData")
	registerFormat    = clipUser.NewProc("RegisterClipboardFormatW")
	formatAvailable   = clipUser.NewProc("IsClipboardFormatAvailable")
	createClipWindow  = clipUser.NewProc("CreateWindowExW")
	destroyClipWindow = clipUser.NewProc("DestroyWindow")
	globalAlloc       = clipKernel.NewProc("GlobalAlloc")
	globalFree        = clipKernel.NewProc("GlobalFree")
	globalSize        = clipKernel.NewProc("GlobalSize")
	globalLock        = clipKernel.NewProc("GlobalLock")
	globalUnlock      = clipKernel.NewProc("GlobalUnlock")
	clipboardOwner    uintptr // main-thread only, no delayed providers
)

const cfDIB = 8
const cfDIBV5 = 17

func nativeFormat(name string) uintptr {
	p, _ := windows.UTF16PtrFromString(name)
	id, _, _ := registerFormat.Call(uintptr(unsafe.Pointer(p)))
	return id
}

// clipboardFormatAvailable distinguishes an offered format from one the system
// is willing to synthesize from another. See windowsImageReadFlavors.
func clipboardFormatAvailable(format uintptr) bool {
	available, _, _ := formatAvailable.Call(format)
	return available != 0
}

// windowsImageFormatID maps a flavor to the native format this platform uses.
func windowsImageFormatID(flavor imageFlavor) uintptr {
	switch flavor {
	case flavorPNG:
		return nativeFormat("PNG")
	case flavorPNGMime:
		return nativeFormat(string(transfer.PNG))
	case flavorDIBV5:
		return cfDIBV5
	case flavorDIB:
		return cfDIB
	}
	return 0
}

// imageFlavorLimit caps the bytes copied out of the OS before decoding.
func imageFlavorLimit(flavor imageFlavor) int {
	if flavor == flavorPNG || flavor == flavorPNGMime {
		return MaxImageBytes
	}
	return MaxNativeImageBytes
}

func copyClipboardFormat(format uintptr, limit int) ([]byte, error) {
	handle, _, _ := getClipboardData.Call(format)
	if handle == 0 {
		return nil, errors.New("没有可读取的图片格式")
	}
	size, _, _ := globalSize.Call(handle)
	if size == 0 || size > uintptr(limit) {
		return nil, errors.New("剪贴板图片超过内存上限")
	}
	address, _, err := globalLock.Call(handle)
	if address == 0 {
		return nil, fmt.Errorf("读取剪贴板图片: %w", err)
	}
	defer globalUnlock.Call(handle)
	data := make([]byte, int(size))
	copy(data, nativeClipboardBytes(address, int(size)))
	return data, nil
}

// Native addresses come from Win32, never from Go's movable object graph.
func nativeClipboardBytes(address uintptr, size int) []byte {
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&address))
	return unsafe.Slice((*byte)(ptr), size)
}

// readNativeImageCandidate copies the next offered image payload, skipping
// flavors already tried. Only the byte copy happens under the clipboard lock:
// decoding a large image there would block other applications' pastes.
func readNativeImageCandidate(tried map[imageFlavor]bool) ([]byte, imageFlavor, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ok, _, _ := openClipboard.Call(0)
	if ok == 0 {
		return nil, flavorPNG, ErrClipboardBusy
	}
	defer closeClipboard.Call()
	for _, flavor := range windowsImageReadFlavors {
		if tried[flavor] {
			continue
		}
		format := windowsImageFormatID(flavor)
		if format == 0 {
			continue
		}
		if flavor == flavorDIBV5 && !clipboardFormatAvailable(format) {
			continue
		}
		raw, err := copyClipboardFormat(format, imageFlavorLimit(flavor))
		if err != nil {
			continue
		}
		return raw, flavor, nil
	}
	return nil, flavorPNG, errNoClipboardImage
}

func readClipboardImage() (Item, error) {
	tried := make(map[imageFlavor]bool, len(windowsImageReadFlavors))
	var decodeErr error
	for {
		raw, flavor, err := readNativeImageCandidate(tried)
		if errors.Is(err, errNoClipboardImage) {
			break
		}
		if err != nil {
			return Item{}, err
		}
		item, err := decodeImageFlavor(flavor, raw)
		if err == nil {
			return item, nil
		}
		// An offered format can still hold a malformed payload. Keep the first
		// reason and try the next format rather than losing the whole snapshot.
		if decodeErr == nil {
			decodeErr = err
		}
		tried[flavor] = true
	}
	if decodeErr != nil {
		return Item{}, decodeErr
	}
	return Item{}, errNoClipboardImage
}

type clipboardHandle struct {
	format uintptr
	memory uintptr
	// payload is copied as-is into the allocation. When dib is set the image is
	// encoded instead, because CF_DIB is a raw bitmap rather than a container.
	payload []byte
	dib     bool
}

func prepareNativeImage(item Item, remote bool) (PreparedWrite, bool, error) {
	release := beginImageWork()
	defer release()
	img := item.pixels
	if img == nil {
		var err error
		img, err = DecodePNG(item.PNG)
		if err != nil {
			return PreparedWrite{}, true, err
		}
	}
	defer ReclaimImageMemory(item.Width, item.Height)
	// The published set comes from windowsImageWriteFlavors; read the comment
	// there before adding a format. Feishu depends on the 24-bit CF_DIB.
	handles := make([]clipboardHandle, 0, len(windowsImageWriteFlavors)+1)
	for _, flavor := range windowsImageWriteFlavors {
		switch flavor {
		case flavorPNG:
			handles = append(handles, clipboardHandle{format: windowsImageFormatID(flavor), payload: item.PNG})
		case flavorDIB:
			handles = append(handles, clipboardHandle{format: windowsImageFormatID(flavor), dib: true})
		default:
			// A flavor added to the table needs an encoder here before it can be
			// published; failing loudly beats publishing a partial set.
			return PreparedWrite{}, true, fmt.Errorf("不支持发布剪贴板图片格式 %v", flavor)
		}
	}
	marker := localFormat
	if remote {
		marker = remoteFormat
	}
	handles = append(handles, clipboardHandle{format: nativeFormat(string(marker)), payload: remoteMarker})
	cleanup := func() {
		for i := range handles {
			if handles[i].memory != 0 {
				globalFree.Call(handles[i].memory)
				handles[i].memory = 0
			}
		}
	}
	// Allocate and populate HGLOBAL on the preparing worker. After success the
	// OS owns these buffers; canceled requests explicitly free them.
	for i := range handles {
		handle := &handles[i]
		if handle.format == 0 {
			cleanup()
			return PreparedWrite{}, true, errors.New("注册剪贴板格式失败")
		}
		size := len(handle.payload)
		if handle.dib {
			size = dibRawSize(img.Bounds().Dx(), img.Bounds().Dy())
		}
		memory, _, allocErr := globalAlloc.Call(0x2, uintptr(size))
		if memory == 0 {
			cleanup()
			return PreparedWrite{}, true, fmt.Errorf("准备剪贴板图片: %w", allocErr)
		}
		handle.memory = memory
		ptr, _, lockErr := globalLock.Call(memory)
		if ptr == 0 {
			cleanup()
			return PreparedWrite{}, true, fmt.Errorf("准备剪贴板图片: %w", lockErr)
		}
		destination := nativeClipboardBytes(ptr, size)
		if handle.dib {
			encodeDIBInto(destination, img)
		} else {
			copy(destination, handle.payload)
		}
		globalUnlock.Call(memory)
	}
	publish := func() error {
		defer crashlog.TrackOperation("发布原生剪贴板图片")()
		result := errors.New("剪贴板线程已停止")
		runOnClipboardThread(func() {
			if clipboardOwner == 0 {
				class, _ := windows.UTF16PtrFromString("STATIC")
				// A hidden message-only owner uses the existing UI loop. No polling,
				// custom window procedure, extra thread or delayed image provider.
				hwnd, _, err := createClipWindow.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, ^uintptr(2), 0, 0, 0)
				if hwnd == 0 {
					result = fmt.Errorf("创建剪贴板窗口: %w", err)
					return
				}
				clipboardOwner = hwnd
				mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
					destroyClipWindow.Call(clipboardOwner)
					clipboardOwner = 0
				})
			}
			opened, _, _ := openClipboard.Call(clipboardOwner)
			if opened == 0 {
				result = ErrClipboardBusy
				return
			}
			defer closeClipboard.Call()
			if ok, _, err := emptyClipboard.Call(); ok == 0 {
				result = fmt.Errorf("清空剪贴板: %w", err)
				return
			}
			for i := range handles {
				if handles[i].memory == 0 {
					result = errors.New("剪贴板图片已发布或取消")
					return
				}
				if ok, _, err := setClipboardData.Call(handles[i].format, handles[i].memory); ok == 0 {
					result = fmt.Errorf("发布剪贴板格式: %w", err)
					return
				}
				handles[i].memory = 0 // ownership transferred to the system
			}
			result = nil
		})
		return result
	}
	return PreparedWrite{Publish: publish, release: cleanup}, true, nil
}
