//go:build !darwin && !windows

package paste

func (t Target) WindowBounds() (Rectangle, bool) { return Rectangle{}, false }
