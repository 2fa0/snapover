package main

import (
	"bytes"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
)

type checkBoard struct{ item clip.Item }

func (b checkBoard) Sequence() (uint64, error)    { return 7, nil }
func (b checkBoard) Snapshot() (clip.Item, error) { return b.item, nil }
func (b checkBoard) Write(clip.Item) error        { panic("diagnostics must not write") }

func TestCheckDoesNotLogClipboardBody(t *testing.T) {
	item, err := clip.FromText("private-token-for-diagnostic-test")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	runCheck(slog.New(slog.NewTextHandler(&out, nil)), checkBoard{item})
	if strings.Contains(out.String(), item.Text) || !strings.Contains(out.String(), "当前是文本") || !strings.Contains(out.String(), "bytes=") {
		t.Fatalf("diagnostic did not retain metadata without the body: %s", out.String())
	}
}

func TestCheckModeLeavesRunningAppDataUntouched(t *testing.T) {
	if os.Getenv("SNAPOVER_TEST_UI") != "1" {
		t.Skip("requires a desktop session; set SNAPOVER_TEST_UI=1")
	}
	dir := t.TempDir()
	live, err := history.Open(filepath.Join(dir, "history"))
	if err != nil {
		t.Fatal(err)
	}
	item, _ := clip.FromText(strings.Repeat("live clipboard body", 100))
	entry, err := live.Add(history.DirLocal, "", item)
	if err != nil {
		t.Fatal(err)
	}
	before := directoryContents(t, dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^$")
	child.Env = append(os.Environ(), "SNAPOVER_TEST_CHECK_HELPER="+dir)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("check subprocess: %v\n%s", err, out)
	} else if bytes.Contains(out, []byte("private-check-body")) {
		t.Fatal("check subprocess exposed the clipboard body")
	}
	if after := directoryContents(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("check startup changed the running application's files")
	}
	if got, err := live.PasteItem(entry.ID); err != nil || got.Text != item.Text {
		t.Fatal("check startup damaged live history", err)
	}
}

func directoryContents(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		out[path] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
