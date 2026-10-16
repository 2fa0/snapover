package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type fakeFileNotification struct {
	id    string
	live  map[string]bool
	shown chan string
}

func (n *fakeFileNotification) Show() error { n.live[n.id] = true; n.shown <- n.id; return nil }
func (n *fakeFileNotification) Close()      { delete(n.live, n.id) }

func TestFileNotificationsAreBoundedAndReleasedAtShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := make(chan fileNotification, maxFileNotifications)
	shown := make(chan string, 1)
	live := make(map[string]bool)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runFileNotifications(ctx, queue, func(id string, n fileNotification) notificationHandle {
			if len(live) >= maxFileNotifications {
				panic("old notification was not released")
			}
			return &fakeFileNotification{id: id, live: live, shown: shown}
		}, nil)
	}()
	for i := 0; i < 100; i++ {
		queue <- fileNotification{body: fmt.Sprint(i)}
		select {
		case id := <-shown:
			if id != fmt.Sprintf("snapover-file-%d", i%maxFileNotifications) {
				t.Fatal(id)
			}
		case <-time.After(time.Second):
			t.Fatal("notification stalled")
		}
	}
	cancel()
	<-done
	if len(live) != 0 {
		t.Fatal("notification callbacks retained at shutdown")
	}
}
