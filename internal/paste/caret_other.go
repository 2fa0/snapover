//go:build !darwin && !windows

package paste

func (t Target) CaretBounds() (Rectangle, bool) { return Rectangle{}, false }
