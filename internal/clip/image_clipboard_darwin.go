//go:build darwin

package clip

import (
	"bytes"
	"errors"
	"unsafe"

	"github.com/ebitengine/purego/objc"
)

var imagePasteboard = func() objc.ID {
	return objc.ID(objc.GetClass("NSPasteboard")).Send(objc.RegisterName("generalPasteboard"))
}

func readClipboardImage() (Item, error) {
	// Read native bytes directly: MyGo's portable PNG read also converts TIFF
	// on the UI thread. Decode, resize, hash and encode only on the watcher.
	var raw []byte
	isPNG := false
	err := errors.New("剪贴板没有可读取的图片")
	runOnClipboardThread(func() {
		pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
		defer pool.Send(objc.RegisterName("drain"))
		pb := imagePasteboard()
		for i, typ := range []string{"public.png\x00", "public.tiff\x00"} {
			name := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), unsafe.StringData(typ))
			data := pb.Send(objc.RegisterName("dataForType:"), name)
			if data == 0 {
				continue
			}
			limit := uint64(MaxNativeImageBytes)
			if i == 0 {
				limit = MaxImageBytes
			}
			length := objc.Send[uint64](data, objc.RegisterName("length"))
			if length == 0 || length > limit {
				err = errors.New("剪贴板原生图片超过内存上限")
				return
			}
			address := objc.Send[unsafe.Pointer](data, objc.RegisterName("bytes"))
			if address == nil {
				return
			}
			raw = bytes.Clone(unsafe.Slice((*byte)(address), int(length)))
			isPNG, err = i == 0, nil
			return
		}
	})
	if err != nil {
		return Item{}, err
	}
	if isPNG {
		return FromPNG(raw)
	}
	return fromTIFF(raw)
}
