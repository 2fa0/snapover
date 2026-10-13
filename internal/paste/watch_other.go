//go:build !darwin && !windows

package paste

import "errors"

func WatchForeground(func()) (func(), error) {
	return nil, errors.New("foreground notifications unsupported")
}
