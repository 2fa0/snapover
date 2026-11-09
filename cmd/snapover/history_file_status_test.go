package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo/ui"
)

func TestFileHistoryAvailabilityRefreshesAfterDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	entry := history.Entry{ID: "file", Kind: "files", FilePaths: []string{path}}
	a := &app{}
	label := ""
	tt := ui.NewTester(func(c *ui.Context) { label = a.fileAvailabilityMessage(c, entry); ui.Text(c, label) }, 600, 160)
	waitFor := func(want string) {
		t.Helper()
		until := time.Now().Add(time.Second)
		for time.Now().Before(until) {
			tt.Frame()
			if strings.Contains(label, want) {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("want %s, got %s", want, label)
	}
	waitFor("文件仍在原路径")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	result := a.fileAvailability[entry.ID]
	result.at = time.Now().Add(-3 * time.Second)
	a.fileAvailability[entry.ID] = result
	waitFor("文件已失效")
}
