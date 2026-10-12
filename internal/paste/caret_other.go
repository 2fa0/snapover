//go:build !darwin

package paste

func (t Target) CaretBounds() (Rectangle, bool) { return Rectangle{}, false }
