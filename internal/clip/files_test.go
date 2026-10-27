package clip

import (
	"bytes"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
)

func TestFilesNativeRepresentationsAndRemoteMarker(t *testing.T) {
	stubClipboardThread(t)
	paths := []string{filepath.Join(t.TempDir(), "中文 #.txt"), filepath.Join(t.TempDir(), "folder")}
	item, err := FromFiles(paths)
	if err != nil {
		t.Fatal(err)
	}
	original := writeClipboardData
	t.Cleanup(func() { writeClipboardData = original })
	var written transfer.Data
	writeClipboardData = func(d transfer.Data, _ ...mygo.ClipboardOptions) error { written = d; return nil }
	p, err := (MyGoBoard{}).PrepareRemoteWrite(item)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Publish(); err != nil {
		t.Fatal(err)
	}
	got, err := written.Files()
	if err != nil || !reflect.DeepEqual(got, paths) {
		t.Fatalf("paths changed: %v %v", got, err)
	}
	mark, err := written.Read(remoteFormat)
	if err != nil || !bytes.Equal(mark, remoteMarker) {
		t.Fatal("remote ownership missing", err)
	}
	if _, err := FromFiles([]string{"relative"}); err == nil {
		t.Fatal("relative reference accepted")
	}
	if !HasFiles([]string{string(transfer.FileList)}) || HasFiles([]string{string(transfer.PNG), string(transfer.Text)}) {
		t.Fatal("file format detection")
	}
}
