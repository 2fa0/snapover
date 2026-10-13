package main

import (
	"testing"

	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/settings"
	clipsync "github.com/2fa0/snapover/internal/sync"
)

func interfaceTestApp(t *testing.T) *app {
	t.Helper()
	dir := t.TempDir()
	store, err := history.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	peers, err := clipsync.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := settings.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	engine := &clipsync.Engine{Peers: peers, Name: "测试电脑"}
	engine.SetReceiveDir(dir)
	return newApp(engine, store, cfg, &identity.Identity{}, ":47821", nil, "")
}
