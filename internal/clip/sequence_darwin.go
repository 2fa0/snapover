//go:build darwin

package clip

import (
	"errors"
	"sync"

	"github.com/ebitengine/purego/objc"
)

// MyGo does not expose pasteboard changeCount. Polling it is cheap and
// avoids decoding an image every 300ms. MyGo owns the AppKit loop, so this
// process must not install its own clipboard listener window.
var (
	seqOnce              sync.Once
	selGeneralPasteboard objc.SEL
	selChangeCount       objc.SEL
)

func platformSequence() (uint64, error) {
	seqOnce.Do(func() {
		selGeneralPasteboard = objc.RegisterName("generalPasteboard")
		selChangeCount = objc.RegisterName("changeCount")
	})
	cls := objc.GetClass("NSPasteboard")
	if cls == 0 {
		return 0, errors.New("NSPasteboard 不可用")
	}
	pb := objc.ID(cls).Send(selGeneralPasteboard)
	if pb == 0 {
		return 0, errors.New("generalPasteboard 不可用")
	}
	n := objc.Send[int64](pb, selChangeCount)
	if n < 0 {
		return 0, errors.New("changeCount 为负")
	}
	return uint64(n), nil
}
