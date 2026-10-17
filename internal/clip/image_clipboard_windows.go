//go:build windows

package clip

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/2fa0/snapover/internal/crashlog"
	"golang.org/x/sys/windows"
)

var clipUser = windows.NewLazySystemDLL("user32.dll")
var clipKernel = windows.NewLazySystemDLL("kernel32.dll")
var (
	openClipboard     = clipUser.NewProc("OpenClipboard")
	closeClipboard    = clipUser.NewProc("CloseClipboard")
	emptyClipboard    = clipUser.NewProc("EmptyClipboard")
	getClipboardData  = clipUser.NewProc("GetClipboardData")
	setClipboardData  = clipUser.NewProc("SetClipboardData")
	registerFormat    = clipUser.NewProc("RegisterClipboardFormatW")
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

func readNativeClipboardImage() ([]byte, bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Win32 byte retrieval is not tied to MyGo's UI thread. There are no image
	// decoders or encoders in the UI callback, including the DIB-only path.
	ok, _, _ := openClipboard.Call(0)
	if ok == 0 {
		return nil, false, ErrClipboardBusy
	}
	defer closeClipboard.Call()
	raw, err := copyClipboardFormat(nativeFormat("PNG"), MaxImageBytes)
	if err != nil {
		raw, err = copyClipboardFormat(nativeFormat(string(transfer.PNG)), MaxImageBytes)
	}
	isPNG := err == nil
	if err != nil {
		raw, err = copyClipboardFormat(cfDIBV5, MaxNativeImageBytes)
		if err != nil {
			raw, err = copyClipboardFormat(cfDIB, MaxNativeImageBytes)
		}
	}
	return raw, isPNG, err
}

func readClipboardImage() (Item, error) {
	raw, isPNG, err := readNativeClipboardImage()
	if err != nil {
		return Item{}, err
	}
	if isPNG {
		return FromPNG(raw)
	}
	release := beginImageWork()
	defer release()
	img, err := decodeDIB(raw)
	if err != nil {
		return Item{}, err
	}
	return itemFromImage(img)
}

type clipboardHandle struct{ format, memory uintptr }

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
	handles := []clipboardHandle{{format: nativeFormat("PNG")}, {format: cfDIB}}
	data := [][]byte{item.PNG, nil}
	if remote {
		handles = append(handles, clipboardHandle{format: nativeFormat(string(remoteFormat))})
		data = append(data, remoteMarker)
	}
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
	for i, b := range data {
		if handles[i].format == 0 {
			cleanup()
			return PreparedWrite{}, true, errors.New("注册剪贴板格式失败")
		}
		size := len(b)
		if i == 1 {
			size = 40 + 4*img.Bounds().Dx()*img.Bounds().Dy()
		}
		handle, _, allocErr := globalAlloc.Call(0x2, uintptr(size))
		if handle == 0 {
			cleanup()
			return PreparedWrite{}, true, fmt.Errorf("准备剪贴板图片: %w", allocErr)
		}
		handles[i].memory = handle
		ptr, _, lockErr := globalLock.Call(handle)
		if ptr == 0 {
			cleanup()
			return PreparedWrite{}, true, fmt.Errorf("准备剪贴板图片: %w", lockErr)
		}
		destination := nativeClipboardBytes(ptr, size)
		if i == 1 {
			encodeDIBInto(destination, img)
		} else {
			copy(destination, b)
		}
		globalUnlock.Call(handle)
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
