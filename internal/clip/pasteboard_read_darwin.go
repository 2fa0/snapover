//go:build darwin

package clip

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Read through independent Pasteboard Manager references on one worker.
// Never share AppKit's cached NSPasteboard object with the UI thread: a lazy
// provider can block while rendering data, and its object state is not safe
// to access concurrently with MyGo's clipboard publication.
var pasteboardReader struct {
	sync.Once
	jobs        chan func()
	err         error
	create      func(objc.ID, *uintptr) int32
	synchronize func(uintptr) uint32
	count       func(uintptr, *uint32) int32
	item        func(uintptr, int64, *uintptr) int32
	data        func(uintptr, uintptr, objc.ID, *uintptr) int32
	length      func(uintptr) int64
	dataBytes   func(uintptr) unsafe.Pointer
	release     func(uintptr)
}

var readPasteboardName = func() objc.ID {
	return objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), unsafe.StringData("com.apple.pasteboard.clipboard\x00"))
}

func readNativePasteboardData(typ string, limit int) ([]byte, error) {
	items, err := readNativePasteboardItems(typ, limit, 1)
	if len(items) == 0 {
		return nil, err
	}
	return items[0], err
}

func readNativePasteboardItems(typ string, limit, maxItems int) ([][]byte, error) {
	pasteboardReader.Do(func() {
		pasteboardReader.jobs = make(chan func())
		go func() {
			runtime.LockOSThread()
			h, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
			pasteboardReader.err = err
			if err == nil {
				purego.RegisterLibFunc(&pasteboardReader.create, h, "PasteboardCreate")
				purego.RegisterLibFunc(&pasteboardReader.synchronize, h, "PasteboardSynchronize")
				purego.RegisterLibFunc(&pasteboardReader.count, h, "PasteboardGetItemCount")
				purego.RegisterLibFunc(&pasteboardReader.item, h, "PasteboardGetItemIdentifier")
				purego.RegisterLibFunc(&pasteboardReader.data, h, "PasteboardCopyItemFlavorData")
				purego.RegisterLibFunc(&pasteboardReader.length, h, "CFDataGetLength")
				purego.RegisterLibFunc(&pasteboardReader.dataBytes, h, "CFDataGetBytePtr")
				purego.RegisterLibFunc(&pasteboardReader.release, h, "CFRelease")
			}
			for job := range pasteboardReader.jobs {
				job()
			}
		}()
	})
	done := make(chan struct{})
	var raw [][]byte
	var err error
	pasteboardReader.jobs <- func() {
		defer close(done)
		if pasteboardReader.err != nil {
			err = pasteboardReader.err
			return
		}
		pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
		defer pool.Send(objc.RegisterName("drain"))
		var board uintptr
		if status := pasteboardReader.create(readPasteboardName(), &board); status != 0 {
			err = fmt.Errorf("打开剪贴板失败：%d", status)
			return
		}
		defer pasteboardReader.release(board)
		pasteboardReader.synchronize(board)
		var count uint32
		if status := pasteboardReader.count(board, &count); status != 0 {
			err = fmt.Errorf("读取剪贴板条目失败：%d", status)
			return
		}
		if count == 0 {
			return
		}
		if maxItems == 1 {
			count = 1
		} else if count > uint32(maxItems) {
			err = errors.New("剪贴板文件数量超过上限")
			return
		}
		total := 0
		for index := uint32(1); index <= count; index++ {
			var value []byte
			value, err = copyPasteboardItem(board, int64(index), typ, limit-total)
			if err != nil {
				return
			}
			total += len(value)
			if len(value) != 0 {
				raw = append(raw, value)
			}
		}
	}
	<-done
	return raw, err
}

func copyPasteboardItem(board uintptr, index int64, typ string, limit int) (raw []byte, err error) {
	var item, data uintptr
	if status := pasteboardReader.item(board, index, &item); status != 0 {
		err = fmt.Errorf("读取剪贴板条目失败：%d", status)
		return
	}
	name := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), unsafe.StringData(typ+"\x00"))
	status := pasteboardReader.data(board, item, name, &data)
	if status == -25133 { // badPasteboardFlavorErr: try the next format.
		return
	}
	if status != 0 {
		err = fmt.Errorf("读取剪贴板数据失败：%d", status)
		return
	}
	if data == 0 {
		return
	}
	defer pasteboardReader.release(data)
	length := pasteboardReader.length(data)
	if length > int64(limit) {
		if typ == "public.utf8-plain-text" {
			err = TextTooLarge(int(length), limit)
		} else {
			err = errors.New("剪贴板数据超过内存上限")
		}
		return
	}
	if length <= 0 {
		return
	}
	address := pasteboardReader.dataBytes(data)
	if address == nil {
		err = errors.New("剪贴板数据不可读")
		return
	}
	if typ == "public.file-url" {
		// Finder and other apps may offer inode-based file reference URLs.
		// Resolve them on this same Foundation worker and autorelease pool.
		text := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("alloc"))
		text = text.Send(objc.RegisterName("initWithBytes:length:encoding:"), address, uint64(length), uint64(4))
		if text == 0 {
			return nil, errors.New("文件剪贴板 URL 编码无效")
		}
		defer text.Send(objc.RegisterName("release"))
		fileURL := objc.ID(objc.GetClass("NSURL")).Send(objc.RegisterName("URLWithString:"), text)
		if fileURL == 0 || !objc.Send[bool](fileURL, objc.RegisterName("isFileURL")) {
			return nil, errors.New("文件剪贴板 URL 无效")
		}
		pathURL := fileURL.Send(objc.RegisterName("filePathURL"))
		if pathURL == 0 {
			return nil, errors.New("无法解析文件剪贴板引用")
		}
		resolved := pathURL.Send(objc.RegisterName("absoluteString"))
		n := objc.Send[uint64](resolved, objc.RegisterName("lengthOfBytesUsingEncoding:"), uint64(4))
		if n > uint64(limit) {
			return nil, errors.New("剪贴板文件路径超过上限")
		}
		ptr := objc.Send[unsafe.Pointer](resolved, objc.RegisterName("UTF8String"))
		if ptr == nil {
			return nil, errors.New("无法读取文件剪贴板引用")
		}
		return bytes.Clone(unsafe.Slice((*byte)(ptr), int(n))), nil
	}
	raw = bytes.Clone(unsafe.Slice((*byte)(address), int(length)))
	return raw, err
}
