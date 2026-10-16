package crashlog

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"time"
)

// TrackOperation records slow completion and, if still blocked after three
// seconds, all Go stacks. Names must never contain clipboard contents.
// Call the returned function once when the operation finishes.
func TrackOperation(name string) func() {
	mu.Lock()
	enabled := sink != nil
	mu.Unlock()
	if !enabled {
		return func() {}
	}
	return trackOperation(name, 500*time.Millisecond, 3*time.Second)
}

func trackOperation(name string, slowAfter, dumpAfter time.Duration) func() {
	name = oneLine(name)
	started := time.Now()
	var finished atomic.Bool
	timer := time.AfterFunc(dumpAfter, func() {
		if finished.Load() {
			return
		}
		// Bound the dump size even if a stalled process has many goroutines.
		stack := make([]byte, 1<<20)
		n := runtime.Stack(stack, true)
		if !finished.Load() {
			writef("%s 操作长时间未返回 name=%s elapsed=%s；以下为 Go 栈，系统休眠也可能使计时延后\n%s\n",
				time.Now().Format(time.RFC3339), name, time.Since(started).Round(time.Millisecond), stack[:n])
		}
	})
	return func() {
		finished.Store(true)
		timer.Stop()
		if elapsed := time.Since(started); elapsed >= slowAfter {
			Breadcrumb(fmt.Sprintf("慢操作结束 name=%s elapsed=%s", name, elapsed.Round(time.Millisecond)))
		}
	}
}
