package paste

import "errors"

var (
	// ErrNeedAccess means the system will not let this process type into
	// another app until the user allows it.
	ErrNeedAccess = errors.New("need accessibility")
	// ErrNoTarget means there is no other window to paste into.
	ErrNoTarget = errors.New("no target")
	// ErrActivate means the previous window could not be focused.
	ErrActivate = errors.New("activate failed")
)

// Target is the app or window that should receive the paste.
type Target struct {
	id uintptr
}

func (t Target) Valid() bool { return t.id != 0 }
