package main

import (
	"time"

	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo/ui"
)

type fileAvailabilityResult struct {
	id        string
	available bool
	at        time.Time
}

// Even a single Stat can block on a disconnected network volume.
func (a *app) fileAvailabilityMessage(c *ui.Context, entry history.Entry) string {
	for pending := a.fileAvailabilityPending; pending != nil; {
		select {
		case result := <-pending:
			if a.fileAvailability == nil || len(a.fileAvailability) >= 128 {
				a.fileAvailability = make(map[string]fileAvailabilityResult)
			}
			a.fileAvailability[result.id] = result
			if a.fileAvailabilityChecking == result.id {
				a.fileAvailabilityChecking = ""
			}
		default:
			pending = nil
		}
	}
	result, found := a.fileAvailability[entry.ID]
	if (!found || time.Since(result.at) > 2*time.Second) && a.fileAvailabilityChecking != entry.ID {
		if a.fileAvailabilityPending == nil {
			a.fileAvailabilityPending = make(chan fileAvailabilityResult, 8)
		}
		pending := a.fileAvailabilityPending
		a.fileAvailabilityChecking = entry.ID
		go func(e history.Entry) {
			pending <- fileAvailabilityResult{id: e.ID, available: history.FilesAvailable(e), at: time.Now()}
		}(entry)
	}
	if a.fileAvailabilityChecking == entry.ID {
		c.After(20 * time.Millisecond)
	}
	if !found {
		return "正在检查文件是否仍可粘贴"
	}
	if !result.available {
		return "文件已失效，请重新复制后再置顶"
	}
	if history.HasFileSnapshot(entry) {
		return "已保存置顶副本，源文件删除后仍可粘贴"
	}
	return "文件仍在原路径时可粘贴；置顶后会保存副本"
}

// Unknown or stale paths are checked by the paste worker, never by a menu.
func (a *app) cachedFilesAvailable(entry history.Entry) bool {
	result, found := a.fileAvailability[entry.ID]
	return !found || time.Since(result.at) > 2*time.Second || result.available
}
