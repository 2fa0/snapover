//go:build !darwin && !windows

package clip

import "errors"

func platformSequence() (uint64, error) {
	return 0, errors.New("剪贴板监听只支持 windows 和 macos")
}
