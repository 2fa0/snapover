//go:build windows

package paste

import (
	"fmt"

	"golang.org/x/sys/windows"
)

var (
	procSetWinEventHook = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent  = user32.NewProc("UnhookWinEvent")
	foregroundWatches   = map[uintptr]func(){}
	foregroundCallback  = windows.NewCallback(func(hook, event, hwnd uintptr, objectID, childID int32, thread, at uint32) uintptr {
		if changed := foregroundWatches[hook]; changed != nil {
			changed()
		}
		return 0
	})
)

// Out-of-context callbacks use the registering thread's existing MyGo loop.
// Register and stop on that same main thread.
func WatchForeground(changed func()) (stop func(), err error) {
	const eventSystemForeground = 3
	const skipOwnProcess = 2
	hook, _, nativeErr := procSetWinEventHook.Call(eventSystemForeground, eventSystemForeground,
		0, foregroundCallback, 0, 0, skipOwnProcess)
	if hook == 0 {
		return nil, fmt.Errorf("SetWinEventHook: %w", nativeErr)
	}
	foregroundWatches[hook] = changed
	return func() {
		if foregroundWatches[hook] != nil {
			procUnhookWinEvent.Call(hook)
			delete(foregroundWatches, hook)
		}
	}, nil
}
