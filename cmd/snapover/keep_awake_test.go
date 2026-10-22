package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/keepawake"
	"github.com/2fa0/snapover/internal/settings"
)

func TestKeepAwakeFailedStartDoesNotPersist(t *testing.T) {
	a := interfaceTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.root = ctx
	a.setKeepAwake(true)
	if a.keepAwake || a.cfg.Values.KeepAwake || a.keepAwakeWorker != nil || a.keepAwakeError == "" {
		t.Fatal("failed start changed preference or hid the error")
	}
}

type fakeKeepAwakeSession struct {
	stops int
}

func (s *fakeKeepAwakeSession) Stop() { s.stops++ }

func (s *fakeKeepAwakeSession) Status() keepawake.Status { return keepawake.Status{} }

func TestKeepAwakeDisableStopsEvenOnSaveFailure(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		a := interfaceTestApp(t)
		a.keepAwake, a.cfg.Values.KeepAwake = true, true
		if err := a.cfg.Save(); err != nil {
			t.Fatal(err)
		}
		dataDir := filepath.Dir(a.cfg.Path)
		worker := &fakeKeepAwakeSession{}
		a.keepAwakeWorker = worker
		if failSave {
			a.cfg.Path = dataDir
		}
		a.setKeepAwake(false)
		if worker.stops != 1 || a.keepAwakeWorker != nil || a.keepAwake || a.cfg.Values.KeepAwake {
			t.Fatalf("disable retained worker or enabled state: failSave=%v stops=%d", failSave, worker.stops)
		}
		if (a.keepAwakeError != "") != failSave || (failSave && !strings.Contains(a.keepAwakeError, "下次启动可能重新开启")) {
			t.Fatalf("incorrect save result: %s", a.keepAwakeError)
		}
		cfg, err := settings.Load(dataDir)
		if err != nil || cfg.Values.KeepAwake != failSave {
			t.Fatal("unexpected persisted preference", err)
		}
	}
}
