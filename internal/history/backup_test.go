package history

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
)

func TestPinnedBackupRoundTrip(t *testing.T) {
	source, _ := Open(t.TempDir())
	text, _ := clip.FromText(strings.Repeat("跨平台\n", 5000))
	for _, item := range []clip.Item{text, mustPNG(t)} {
		e, err := source.Add(DirLocal, "private-device", item)
		if err != nil {
			t.Fatal(err)
		}
		if err := source.SetPinned(e.ID, true); err != nil {
			t.Fatal(err)
		}
		if err := source.SetNote(e.ID, "我的备注"); err != nil {
			t.Fatal(err)
		}
	}
	unpinned, _ := clip.FromText("not exported")
	source.Add(DirLocal, "", unpinned)
	path := filepath.Join(t.TempDir(), "pins.zip")
	if n, err := source.ExportPinned(context.Background(), path); err != nil || n != 2 {
		t.Fatalf("export %d: %v", n, err)
	}
	dest, _ := Open(t.TempDir())
	if n, err := dest.ImportPinned(context.Background(), path); err != nil || n != 2 {
		t.Fatalf("import %d: %v", n, err)
	}
	for _, e := range dest.List() {
		if !e.Pinned || e.Note != "我的备注" || e.Peer != "" || e.SourceApp != "" {
			t.Fatalf("metadata: %+v", e)
		}
		got, err := dest.PasteItem(e.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind == clip.KindText && got.Text != text.Text {
			t.Fatal("full text lost")
		}
		if got.Kind == clip.KindImage && !bytes.Equal(got.PNG, mustPNG(t).PNG) {
			t.Fatal("image changed")
		}
	}
	reopened, err := Open(dest.dir)
	if err != nil || len(reopened.List()) != 2 || !reopened.List()[0].Pinned {
		t.Fatal("import did not persist", err)
	}
}

func TestMalformedBackupDoesNotImportPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.zip")
	f, _ := os.Create(path)
	z := zip.NewWriter(f)
	for _, record := range []struct{ name, body string }{{"snapover-pins-v1", "1\n"}, {"0.json", `{"kind":"text","text":"valid"}`}, {"1.json", `{"kind":"image","png":"AAAA"}`}} {
		w, _ := z.Create(record.name)
		w.Write([]byte(record.body))
	}
	z.Close()
	f.Close()
	s, _ := Open(t.TempDir())
	if n, err := s.ImportPinned(context.Background(), path); err == nil || n != 0 || len(s.List()) != 0 {
		t.Fatal("invalid archive modified history", n, err)
	}
}

func TestPinnedBackupMoreThanThousandAndRange(t *testing.T) {
	source, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxEntries+1; i++ {
		item, _ := clip.FromText(fmt.Sprintf("pin-%d", i))
		if _, err := source.add(DirLocal, "", item, true, "note"); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "pins.zip")
	if n, err := source.ExportPinned(context.Background(), path); err != nil || n != MaxEntries+1 {
		t.Fatal(n, err)
	}
	dest, _ := Open(t.TempDir())
	if n, err := dest.ImportPinned(context.Background(), path); err != nil || n != MaxEntries+1 {
		t.Fatal(n, err)
	}
	if _, err := source.ExportPinnedRange(context.Background(), path, 0, 2); err == nil {
		t.Fatal("invalid range accepted")
	}
	if n, err := source.ExportPinnedRange(context.Background(), path, 2, 3); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	record, _, err := readBackupRecord(z.File[1], false)
	if err != nil || record.Text != source.pinnedEntries()[1].Text {
		t.Fatal(record, err)
	}
}

func TestPinnedImportSurvivesConcurrentClear(t *testing.T) {
	source, _ := Open(t.TempDir())
	for i := 0; i < 30; i++ {
		item, _ := clip.FromText(fmt.Sprintf("pin-%d", i))
		if _, err := source.add(DirLocal, "", item, true, "keep this note"); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "pins.zip")
	if _, err := source.ExportPinned(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	dest, _ := Open(t.TempDir())
	done := make(chan struct{})
	failures := make(chan error, 1)
	go func() {
		for {
			select {
			case <-done:
				failures <- nil
				return
			default:
			}
			if err := dest.Clear(); err != nil {
				failures <- err
				return
			}
			runtime.Gosched()
		}
	}()
	n, err := dest.ImportPinned(context.Background(), path)
	close(done)
	clearErr := <-failures
	if err != nil || clearErr != nil || n != 30 {
		t.Fatal(n, err, clearErr)
	}
	reopened, err := Open(dest.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.List()) != 30 {
		t.Fatal("imported pins lost")
	}
	for _, e := range reopened.List() {
		if !e.Pinned || e.Note != "keep this note" {
			t.Fatal(e)
		}
	}
}

func TestPinnedImportReportsOnlyCompletedPrefix(t *testing.T) {
	source, _ := Open(t.TempDir())
	for _, text := range []string{strings.Repeat("x", 30), "first"} {
		item, _ := clip.FromText(text)
		if _, err := source.add(DirLocal, "", item, true, "note"); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "pins.zip")
	if _, err := source.ExportPinned(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	dest, _ := Open(t.TempDir())
	if err := dest.SetCapacity(20); err != nil {
		t.Fatal(err)
	}
	n, err := dest.ImportPinned(context.Background(), path)
	if err == nil || n != 1 {
		t.Fatal(n, err)
	}
	entries := dest.List()
	if len(entries) != 1 || !entries[0].Pinned || entries[0].Note != "note" {
		t.Fatal(entries)
	}
}

func TestBackupValidationDoesNotBuildImageMetadata(t *testing.T) {
	source, _ := Open(t.TempDir())
	if _, err := source.add(DirLocal, "", mustPNG(t), true, ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pins.zip")
	if _, err := source.ExportPinned(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	_, item, err := readBackupRecord(z.File[1], false)
	if err != nil {
		t.Fatal(err)
	}
	if item.Hash != ([32]byte{}) || len(item.Thumbnail) != 0 || len(item.Preview) != 0 {
		t.Fatal("validation built discarded metadata")
	}
}
