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
	e.SetPrivacyPolicy(true, nil, false)
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
	e.SetPrivacyPolicy(false, nil, false)
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
	e.SetPrivacyPolicy(false, []AppRule{{Name: " Safari "}}, false)
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
	e.SetPrivacyPolicy(true, nil, false)
	e.SetPrivacyPolicy(false, nil, false)
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

func TestAppRuleMatchesIdentifierBeforeName(t *testing.T) {
	e := testEngine(t, &memBoard{})
	e.SetPrivacyPolicy(false, []AppRule{{ID: "com.apple.Safari", Name: "Safari"}}, false)
	var recorded int
	e.OnNotice = func(n Notice) error {
		if n.Kind == NoticeLocal {
			recorded++
		}
		return nil
	}
	renamed := mustText(t, "renamed")
	renamed.SourceApp, renamed.SourceID = "Safari浏览器", "com.apple.Safari"
	e.HandleLocal(1, renamed)
	if recorded != 0 {
		t.Fatal("identifier did not match after a rename")
	}
	// A different app that borrowed the name stays out of another's rule.
	renamed.SourceID = "com.google.Chrome"
	e.HandleLocal(2, renamed)
	if recorded != 1 {
		t.Fatal("name matched an app the rule is not about")
	}
}

func TestWhitelistKeepsOnlyListedApps(t *testing.T) {
	b := &memBoard{}
	e := testEngine(t, b)
	e.sess = newSession(nil, "peer", true, discardLog(), nil)
	e.SetPrivacyPolicy(false, []AppRule{{ID: "com.apple.Safari", Name: "Safari"}}, true)
	var recorded int
	e.OnNotice = func(n Notice) error {
		if n.Kind == NoticeLocal {
			recorded++
		}
		return nil
	}
	kept := mustText(t, "from safari")
	kept.SourceApp, kept.SourceID = "Safari", "com.apple.Safari"
	e.HandleLocal(1, kept)
	if recorded != 1 || len(e.sess.out) != 1 {
		t.Fatal("listed app was not recorded and sent")
	}
	dropped := mustText(t, "from notes")
	dropped.SourceApp, dropped.SourceID = "Notes", "com.apple.Notes"
	e.HandleLocal(2, dropped)
	if recorded != 1 || len(e.sess.out) != 1 {
		t.Fatal("unlisted app escaped the whitelist")
	}
}

func TestWhitelistStopsWhenTurnedOff(t *testing.T) {
	e := testEngine(t, &memBoard{})
	e.sess = newSession(nil, "peer", true, discardLog(), nil)
	e.SetPrivacyPolicy(false, []AppRule{{Name: "Safari"}}, true)
	item := mustText(t, "from notes")
	item.SourceApp = "Notes"
	e.HandleLocal(1, item)
	if len(e.sess.out) != 0 {
		t.Fatal("unlisted app sent while the whitelist was on")
	}
	e.SetPrivacyPolicy(false, nil, false)
	e.HandleLocal(2, item)
	if len(e.sess.out) != 1 {
		t.Fatal("copy stayed suppressed after the whitelist ended")
	}
}

func TestNameOnlyRuleStillMatchesAppsWithoutIdentifier(t *testing.T) {
	e := testEngine(t, &memBoard{})
	e.SetPrivacyPolicy(false, []AppRule{{Name: "Snapover CLI"}}, false)
	var recorded int
	e.OnNotice = func(n Notice) error {
		if n.Kind == NoticeLocal {
			recorded++
		}
		return nil
	}
	item := mustText(t, "cli")
	item.SourceApp = "snapover cli"
	e.HandleLocal(1, item)
	if recorded != 0 {
		t.Fatal("name-only rule did not match an app without an identifier")
	}
}
