//go:build windows

package clip

import (
	"fmt"
	"runtime"
	"time"

	"github.com/2fa0/snapover/internal/crashlog"
)

const cfUnicodeText = 13

func readLimitedClipboardText(limit int) (string, error) {
	limit = normalizeTextLimit(limit)
	started := time.Now()
	defer func() {
		crashlog.Breadcrumb("读取剪贴板文本耗时 " + time.Since(started).Round(time.Millisecond).String())
	}()
	// Win32 clipboard reads stay off MyGo's UI thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ok, _, _ := openClipboard.Call(0)
	if ok == 0 {
		return "", ErrClipboardBusy
	}
	defer closeClipboard.Call()
	handle, _, _ := getClipboardData.Call(cfUnicodeText)
	if handle == 0 {
		return "", nil
	}
	size, _, _ := globalSize.Call(handle)
	if size <= 2 {
		return "", nil
	}
	// UTF-8 is at least half the UTF-16 byte count for BMP text. Larger native
	// data cannot fit the limit, so it is not copied into Go.
	if int(size) > 2*limit+2 {
		return "", TextTooLarge(int(size)/2, limit)
	}
	address, _, err := globalLock.Call(handle)
	if address == 0 {
		return "", fmt.Errorf("读取剪贴板文本: %w", err)
	}
	defer globalUnlock.Call(handle)
	raw := make([]byte, int(size))
	copy(raw, nativeClipboardBytes(address, int(size)))
	return decodeUTF16Limited(raw, limit)
}
