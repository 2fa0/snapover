package sync

import (
	"context"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/clip"
)

func TestPrivacyBlocksLocalRemoteAndCLIThenResumes(t *testing.T) {
	b := &memBoard{seq: 10}
	e := testEngine(t, b)
	e.sess = newSession(nil, "peer", true, discardLog(), nil)
	var recorded int
	e.OnNotice = func(n Notice) error {
		if n.Kind == NoticeLocal || n.Kind == NoticeRemote {
			recorded++
		}
		return nil
	}
	e.SetPrivacyPolicy(true, nil)
	if err := e.notify(Notice{Kind: NoticeLocal, Item: mustText(t, "late result")}); err == nil {
		t.Fatal("late local result recorded during privacy mode")
	}
	item := mustText(t, "secret")
	e.ObserveLocal(11, item)
	e.HandleLocal(11, item)
	if err := e.deliverFrom(nil, item); err == nil {
		t.Fatal("remote accepted during privacy mode")
	}
	if err := e.SendClipboard(context.Background(), "peer", item); err == nil {
		t.Fatal("CLI accepted during privacy mode")
	}
	if recorded != 0 || len(e.sess.out) != 0 || len(b.Writes()) != 0 {
		t.Fatal("private content escaped")
	}
	e.SetPrivacyPolicy(false, nil)
	e.HandleLocal(11, item)
	if recorded != 0 {
		t.Fatal("suppressed generation became history after privacy ended")
	}
	e.HandleLocal(12, item)
	if recorded != 1 || len(e.sess.out) != 1 {
		t.Fatal("fresh copy failed after privacy ended")
	}
}

func TestExcludedAppMatchesWholeNameAndDoesNotFilterRemote(t *testing.T) {
	b := &memBoard{}
	e := testEngine(t, b)
	e.SetPrivacyPolicy(false, []string{" Safari "})
	var local int
	e.OnNotice = func(n Notice) error {
		if n.Kind == NoticeLocal {
			local++
		}
		return nil
	}
	item := mustText(t, "text")
	item.SourceApp = "safari"
	e.HandleLocal(1, item)
	if local != 0 {
		t.Fatal("excluded copy recorded")
	}
	e.mu.Lock()
	e.localActive = time.Time{}
	e.mu.Unlock()
	if err := e.deliverFrom(nil, item); err != nil {
		t.Fatal(err)
	}
	if len(b.Writes()) != 1 {
		t.Fatal("remote source name incorrectly filtered")
	}
	item.SourceApp = "Safari Technology Preview"
	e.HandleLocal(2, item)
	if local != 1 {
		t.Fatal("partial app name matched")
	}
}

func TestPolicyChangeDropsPreviouslyQueuedCopies(t *testing.T) {
	e := testEngine(t, &memBoard{})
	e.localQueue = make(chan localJob, 8)
	e.ObserveLocal(1, mustText(t, "old"))
	job := <-e.localQueue
	e.SetPrivacyPolicy(true, nil)
	e.SetPrivacyPolicy(false, nil)
	e.mu.Lock()
	same := job.epoch == e.privacyEpoch
	e.mu.Unlock()
	if same {
		t.Fatal("queued copy survived policy change")
	}
	if ok, _ := e.admitClipboard(queuedItem{Item: clip.Item{SourceApp: "Safari"}}); !ok {
		t.Fatal("normal item rejected")
	}
}
