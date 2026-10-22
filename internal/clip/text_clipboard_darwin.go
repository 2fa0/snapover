//go:build darwin

package clip

import (
	"time"

	"github.com/2fa0/snapover/internal/crashlog"
)

func readLimitedClipboardText(limit int) (string, error) {
	started := time.Now()
	defer func() {
		crashlog.Breadcrumb("读取剪贴板文本耗时 " + time.Since(started).Round(time.Millisecond).String())
	}()
	raw, err := readNativePasteboardData("public.utf8-plain-text", normalizeTextLimit(limit))
	return string(raw), err
}
