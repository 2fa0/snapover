//go:build windows

package clip

import (
	"encoding/binary"
	"errors"
	"runtime"
	"unicode/utf16"
)

func readClipboardFiles() ([]string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ok, _, _ := openClipboard.Call(0)
	if ok == 0 {
		return nil, ErrClipboardBusy
	}
	defer closeClipboard.Call()
	available, _, _ := clipUser.NewProc("IsClipboardFormatAvailable").Call(15)
	if available == 0 {
		return nil, nil
	}
	present, _, _ := getClipboardData.Call(15) // CF_HDROP
	if present == 0 {
		return nil, ErrClipboardBusy
	}
	raw, err := copyClipboardFormat(15, MaxFileListBytes+20)
	if err != nil {
		return nil, err
	}
	if len(raw) < 20 {
		return nil, errors.New("文件剪贴板格式无效")
	}
	offset := int(binary.LittleEndian.Uint32(raw[:4]))
	if offset < 20 || offset > len(raw)-2 || binary.LittleEndian.Uint32(raw[16:20]) == 0 {
		return nil, errors.New("文件剪贴板不是 Unicode 文件列表")
	}
	var paths []string
	var current []uint16
	for i := offset; i+1 < len(raw); i += 2 {
		c := binary.LittleEndian.Uint16(raw[i : i+2])
		if c != 0 {
			current = append(current, c)
			continue
		}
		if len(current) == 0 {
			return paths, nil
		}
		paths = append(paths, string(utf16.Decode(current)))
		current = current[:0]
		if len(paths) > MaxFilePaths {
			return nil, errors.New("剪贴板文件数量超过 10000 项")
		}
	}
	return nil, errors.New("文件剪贴板列表未完整结束")
}
