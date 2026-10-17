//go:build darwin

package clip

import (
	"errors"
	"time"
	"unsafe"

	"github.com/ebitengine/purego/objc"
	"github.com/2fa0/snapover/internal/crashlog"
)

func readLimitedClipboardText(limit int) (string, error) {
	limit = normalizeTextLimit(limit)
	var text string
	err := errors.New("剪贴板线程已停止")
	started := time.Now()
	runOnClipboardThread(func() {
		defer func() {
			crashlog.Breadcrumb("读取剪贴板文本耗时 " + time.Since(started).Round(time.Millisecond).String())
		}()
		pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
		defer pool.Send(objc.RegisterName("drain"))
		name := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), unsafe.StringData("public.utf8-plain-text\x00"))
		data := objc.ID(objc.GetClass("NSPasteboard")).Send(objc.RegisterName("generalPasteboard")).Send(objc.RegisterName("dataForType:"), name)
		if data == 0 {
			text, err = "", nil
			return
		}
		length := objc.Send[uint64](data, objc.RegisterName("length"))
		if length == 0 {
			text, err = "", nil
			return
		}
		if length > uint64(limit) {
			text, err = "", TextTooLarge(int(length), limit)
			return
		}
		address := objc.Send[unsafe.Pointer](data, objc.RegisterName("bytes"))
		if address == nil {
			text, err = "", errors.New("剪贴板文本不可读")
			return
		}
		text, err = string(unsafe.Slice((*byte)(address), int(length))), nil
	})
	return text, err
}
