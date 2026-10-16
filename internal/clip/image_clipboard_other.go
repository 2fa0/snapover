//go:build !windows

package clip

func prepareNativeImage(Item, bool) (PreparedWrite, bool, error) {
	return PreparedWrite{}, false, nil
}
