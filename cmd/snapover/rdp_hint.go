package main

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

func rdpClipboardOwner(name string) bool {
	name = strings.ReplaceAll(name, "\\", "/")
	return strings.EqualFold(filepath.Base(name), "rdpclip.exe")
}

func (a *app) watchRDPOverwrite() {
	a.rdpWatchSeq++
	seq := a.rdpWatchSeq
	owner := a.clipboardOwner
	if owner == nil {
		owner = clip.OwnerProcess
	}
	var stop <-chan struct{}
	if a.root != nil {
		stop = a.root.Done()
	}
	go func() {
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-stop:
			return
		}
		name := owner()
		a.update(func() {
			if a.rdpWatchSeq != seq {
				return
			}
			if rdpClipboardOwner(name) {
				a.setSyncStage(syncStageRDP)
			}
		})
	}()
}
