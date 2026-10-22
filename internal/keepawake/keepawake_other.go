//go:build !windows

package keepawake

import "errors"

const Supported = false

func platformBackend() (backend, error) {
	return nil, errors.New("防止自动锁屏仅支持 Windows")
}
