package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestUpdateSourceMatchesBuildConfig keeps the feed this source computes equal
// to the one `go tool mygo build` compiles into the app from mygo.json. A
// mismatch costs a failed check, not a bad install: the source falls back to
// the updater's own feed, and mygo still verifies the signature.
func TestUpdateSourceMatchesBuildConfig(t *testing.T) {
	data, err := os.ReadFile("../../mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Updates struct {
			GitHub    string `json:"github"`
			URL       string `json:"url"`
			TagPrefix string `json:"tagPrefix"`
		}
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	switch {
	case cfg.Updates.URL != "":
		t.Fatalf("mygo.json serves updates from updates.url %q; updateFeed has to serve them too", cfg.Updates.URL)
	case cfg.Updates.TagPrefix != "" && cfg.Updates.TagPrefix != "v":
		t.Fatalf("mygo.json sets updates.tagPrefix %q, which updateFeed does not resolve", cfg.Updates.TagPrefix)
	}
	name := "update-" + runtime.GOOS + "-" + runtime.GOARCH + ".json"
	want := "https://github.com/" + cfg.Updates.GitHub + "/releases/latest/download/" + name
	if got := updateFeed(); got != want {
		t.Fatalf("updateFeed() = %q, want %q", got, want)
	}
}

// TestUpdateSourceRetriesTransientStatus covers the retry the updater's own
// requests make: a gateway in front of the release files failing for a moment
// is asked again, a missing feed is not.
func TestUpdateSourceRetriesTransientStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   string
	}{
		{"gateway failing for now", http.StatusBadGateway, "0.10.4"},
		{"missing feed", http.StatusNotFound, ""},
	}
	for _, tc := range tests {
		var hits int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits++
			if hits < 2 {
				w.WriteHeader(tc.status)
				return
			}
			io.WriteString(w, `{"version":"0.10.4","url":"u","signature":"s","size":1}`)
		}))
		src := &updateSource{feed: srv.URL}
		body, err := src.Manifest(context.Background(), false)
		srv.Close()
		if tc.want == "" {
			if err == nil {
				body.Close()
				t.Fatalf("%s: got a body, want the error", tc.name)
			}
			if hits != 1 {
				t.Fatalf("%s: asked %d times, want 1", tc.name, hits)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var m struct{ Version string }
		err = json.NewDecoder(body).Decode(&m)
		body.Close()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if m.Version != tc.want {
			t.Fatalf("%s: version = %q, want %q", tc.name, m.Version, tc.want)
		}
		if hits != 2 {
			t.Fatalf("%s: asked %d times, want 2", tc.name, hits)
		}
	}
}

// TestUpdateSourceManifestTimesOut keeps a feed that accepts the connection
// and never answers from holding a check open.
func TestUpdateSourceManifestTimesOut(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer srv.Close()
	defer close(block)

	src := &updateSource{feed: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := src.Manifest(ctx, false); err == nil {
		t.Fatal("a feed that never answers returned no error")
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("waited %v for a canceled check", waited)
	}
}

// TestStallGuardAbandonsDownload covers a release file that stops arriving:
// the download fails instead of waiting for the socket.
func TestStallGuardAbandonsDownload(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		io.WriteString(w, "start")
		w.(http.Flusher).Flush()
		<-block
	}))
	defer srv.Close()
	defer close(block)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := updateGet(ctx, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	guard := newStallGuard(resp, cancel, 60*time.Millisecond)
	defer guard.Close()

	start := time.Now()
	if _, err := io.Copy(io.Discard, guard); err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("download of a stalled body ended with %v", err)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("waited %v for a stalled download", waited)
	}
	if ctx.Err() == nil {
		t.Fatal("a stalled download did not cancel its request")
	}
}

// TestStallGuardLeavesSlowDownloads keeps a download that keeps arriving
// alive, however long it takes.
func TestStallGuardLeavesSlowDownloads(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; i < 10; i++ {
			io.WriteString(w, "x")
			w.(http.Flusher).Flush()
			<-ticker.C
		}
		io.WriteString(w, "x")
		<-block
	}))
	defer srv.Close()
	defer close(block)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := updateGet(ctx, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	guard := newStallGuard(resp, cancel, 300*time.Millisecond)
	defer guard.Close()

	got, err := io.ReadAll(io.LimitReader(guard, 10))
	if err != nil {
		t.Fatalf("a download that keeps arriving failed: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("read %d bytes, want 10", len(got))
	}
	if ctx.Err() != nil {
		t.Fatal("a download that keeps arriving was canceled")
	}
}

// TestStaleUpdateDirsOnlyClearsLeftovers keeps the cleanup away from what an
// update is using, from what the updater renamed aside, and from anything that
// is not an update's.
func TestStaleUpdateDirsOnlyClearsLeftovers(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	mkdir := func(name string, mtime time.Time) {
		path := filepath.Join(dir, name)
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	mkdir(".Snapover.app.update-1234", old) // a download that was killed
	mkdir(".Snapover.update-5678", old)     // the same on Windows
	mkdir(".mygo-update-9012", old)         // a probe that was killed
	mkdir(".Snapover.app.update-9999", time.Now())
	mkdir(".Snapover.app.old-4321", old) // the updater's to remove
	mkdir("resources", old)

	var names []string
	for _, path := range staleUpdateDirs(dir, time.Now(), updateLeftoverAge) {
		names = append(names, filepath.Base(path))
	}
	want := ".Snapover.app.update-1234,.Snapover.update-5678,.mygo-update-9012"
	if strings.Join(names, ",") != want {
		t.Fatalf("staleUpdateDirs() = %v, want %s", names, want)
	}
}

// TestAppDirIsBesideTheApp keeps the cleanup looking where the updater leaves
// its directories: beside the bundle on macOS, the executable's own directory
// elsewhere.
func TestAppDirIsBesideTheApp(t *testing.T) {
	tests := []struct{ exe, goos, want string }{
		{"/Applications/Snapover.app/Contents/MacOS/Snapover", "darwin", "/Applications"},
		{"/Users/me/Snapover.app/Contents/MacOS/snapover", "darwin", "/Users/me"},
		{"/opt/snapover/Snapover", "linux", "/opt/snapover"},
		{"/usr/local/bin/snapover", "darwin", ""},
	}
	for _, tc := range tests {
		if got := appDirOf(tc.exe, tc.goos); got != tc.want {
			t.Fatalf("appDirOf(%q, %s) = %q, want %q", tc.exe, tc.goos, got, tc.want)
		}
	}
}
