//go:build windows

package clip

import "golang.org/x/sys/windows"

// GetClipboardSequenceNumber changes when the clipboard owner changes.
// MyGo already runs the Win32 loop, so a message-only listener would fight it.
var procClipboardSequence = windows.NewLazySystemDLL("user32.dll").NewProc("GetClipboardSequenceNumber")

func platformSequence() (uint64, error) {
	r, _, err := procClipboardSequence.Call()
	if r == 0 && err != nil && err != windows.ERROR_SUCCESS {
		return 0, err
	}
	return uint64(r), nil
}
