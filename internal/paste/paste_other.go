//go:build !darwin && !windows

package paste

func Capture() Target { return Target{} }

func Allowed() bool { return false }

func Prompt() {}

func (t Target) Paste() error {
	if !t.Valid() {
		return ErrNoTarget
	}
	return ErrNeedAccess
}
