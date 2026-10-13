//go:build darwin

package paste

import (
	"errors"
	"sync"

	"github.com/ebitengine/purego/objc"
)

// WatchForeground registers on the main thread; stop must run there too.
func WatchForeground(changed func()) (stop func(), err error) {
	ws := objc.ID(objc.GetClass("NSWorkspace")).Send(selSharedWorkspace)
	center := ws.Send(objc.RegisterName("notificationCenter"))
	if center == 0 {
		return nil, errors.New("NSWorkspace notification center unavailable")
	}
	queue := objc.ID(objc.GetClass("NSOperationQueue")).Send(objc.RegisterName("mainQueue"))
	block := objc.NewBlock(func(_ objc.Block, _ objc.ID) { changed() })
	defer block.Release()
	observer := center.Send(objc.RegisterName("addObserverForName:object:queue:usingBlock:"),
		nsString("NSWorkspaceDidActivateApplicationNotification"), objc.ID(0), queue, block)
	if observer == 0 {
		return nil, errors.New("foreground observer registration failed")
	}
	observer.Send(objc.RegisterName("retain"))
	var once sync.Once
	return func() {
		once.Do(func() {
			center.Send(objc.RegisterName("removeObserver:"), observer)
			observer.Send(objc.RegisterName("release"))
		})
	}, nil
}
