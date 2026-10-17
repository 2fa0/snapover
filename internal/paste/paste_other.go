//go:build !darwin && !windows

package paste

func Capture() Target { return Target{} }

func Allowed() bool { return false }

func Prompt() {}

func (t Target) Activate() error {
	if !t.Valid() {
		return ErrNoTarget
	}
	return ErrNeedAccess
}

func (t Target) SendPaste() error { return ErrNeedAccess }

func ModifiersDown() bool        { return false }
func (t Target) SendCopy() error { return ErrNeedAccess }
