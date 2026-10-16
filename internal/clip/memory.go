package clip

import (
	"runtime/debug"
	"sync"
	"time"
)

// Full-size decodes share one budget across capture, receive and history paste.
// Compressed payloads and small previews do not retain this slot.
var imageWork = make(chan struct{}, 1)

func beginImageWork() func() {
	imageWork <- struct{}{}
	return func() { <-imageWork }
}

var reclaim struct {
	sync.Mutex
	timer *time.Timer
	last  time.Time
}

const imageIdle = 2 * time.Second
const reclaimInterval = 15 * time.Second

// ReclaimImageMemory waits for an idle interval instead of forcing a collection
// in the middle of every large-image burst. At most one timer and one full-size
// image operation exist, and reclamation never competes with a decode.
func ReclaimImageMemory(width, height int) {
	if width <= 0 || height <= 0 || uint64(width)*uint64(height)*4 < 32<<20 {
		return
	}
	reclaim.Lock()
	defer reclaim.Unlock()
	if reclaim.timer != nil {
		reclaim.timer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(imageIdle, func() { reclaimIdle(timer) })
	reclaim.timer = timer
}

func reclaimIdle(timer *time.Timer) {
	reclaim.Lock()
	defer reclaim.Unlock()
	if reclaim.timer != timer {
		return
	}
	delay := time.Until(reclaim.last.Add(reclaimInterval))
	if delay <= 0 {
		select {
		case imageWork <- struct{}{}:
			debug.FreeOSMemory()
			<-imageWork
			reclaim.last = time.Now()
			reclaim.timer = nil
			return
		default:
			delay = imageIdle
		}
	}
	var next *time.Timer
	next = time.AfterFunc(delay, func() { reclaimIdle(next) })
	reclaim.timer = next
}
