package clip

import (
	"sync"
	"time"
)

// EchoGuard drops clipboard events caused by our own remote write.
// A remote write wakes the local watcher; without this, two machines
// bounce the same item forever.
type EchoGuard struct {
	ttl time.Duration
	now func() time.Time

	mu sync.Mutex
	m  map[[32]byte]time.Time
}

func NewEchoGuard(ttl time.Duration) *EchoGuard {
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	return &EchoGuard{ttl: ttl, now: time.Now, m: map[[32]byte]time.Time{}}
}

func (g *EchoGuard) Mark(h [32]byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gcLocked()
	if len(g.m) >= 128 {
		var oldestK [32]byte
		var oldest time.Time
		first := true
		for k, t := range g.m {
			if first || t.Before(oldest) {
				oldestK, oldest, first = k, t, false
			}
		}
		delete(g.m, oldestK)
	}
	g.m[h] = g.now()
}

func (g *EchoGuard) IsEcho(h [32]byte) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.m[h]
	if !ok {
		return false
	}
	if g.now().Sub(t) >= g.ttl {
		delete(g.m, h)
		return false
	}
	return true
}

func (g *EchoGuard) gcLocked() {
	now := g.now()
	for k, t := range g.m {
		if now.Sub(t) >= g.ttl {
			delete(g.m, k)
		}
	}
}
