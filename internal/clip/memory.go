package clip

import (
	"runtime/debug"
	"sync/atomic"
	"time"
)

var imageReclaimPending atomic.Bool

// ReclaimImageMemory returns large temporary pixel buffers to the OS even if
// the app becomes idle. Small images keep the normal GC policy. Requests are
// coalesced and deferred off the UI thread to let callers drop their references.
func ReclaimImageMemory(width, height int) {
	if width <= 0 || height <= 0 || uint64(width)*uint64(height)*4 < 32<<20 {
		return
	}
	if !imageReclaimPending.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(100*time.Millisecond, func() {
		debug.FreeOSMemory()
		imageReclaimPending.Store(false)
	})
}
