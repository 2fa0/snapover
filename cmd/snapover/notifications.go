package main

import (
	"context"
	"fmt"

	"github.com/egoist/mygo"
)

const maxFileNotifications = 16

type fileNotification struct{ title, body, path string }
type notificationHandle interface {
	Show() error
	Close()
}

func (a *app) queueFileNotification(n fileNotification) {
	if a.root == nil || !mygo.NotificationsSupported() {
		return
	}
	n.title = a.tr(n.title)
	n.body = a.tr(n.body)
	a.notificationOnce.Do(func() {
		a.notificationQueue = make(chan fileNotification, maxFileNotifications)
		go runFileNotifications(a.root, a.notificationQueue, func(id string, n fileNotification) notificationHandle {
			notice := mygo.NewNotification(mygo.NotificationOptions{ID: id, Title: n.title, Body: n.body})
			if n.path != "" {
				notice.OnClick(func() { go mygo.Shell.ShowItemInFolder(n.path) })
			}
			return notice
		}, func(err error) {
			if a.engine.Log != nil {
				a.engine.Log.Debug("文件通知未显示", "err", err)
			}
		})
	})
	// Permission prompts and a slow notification service cannot create a new
	// goroutine per transfer or an unbounded backlog of file paths.
	select {
	case <-a.root.Done():
	case a.notificationQueue <- n:
	default:
	}
}

func runFileNotifications(ctx context.Context, queue <-chan fileNotification, newNotice func(string, fileNotification) notificationHandle, onError func(error)) {
	var active [maxFileNotifications]notificationHandle
	defer func() {
		for _, n := range active {
			if n != nil {
				n.Close()
			}
		}
	}()
	slot := 0
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-queue:
			if ctx.Err() != nil {
				return
			}
			if active[slot] != nil {
				active[slot].Close()
			}
			notice := newNotice(fmt.Sprintf("snapover-file-%d", slot), n)
			active[slot] = notice
			if err := notice.Show(); err != nil && onError != nil {
				onError(err)
			}
			slot = (slot + 1) % maxFileNotifications
		}
	}
}
