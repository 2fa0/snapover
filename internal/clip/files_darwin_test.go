//go:build darwin

package clip

import (
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego/objc"
)

func TestNativeFilesReadOnPrivatePasteboard(t *testing.T) {
	pb := privateReadPasteboard(t)
	paths := []string{filepath.Join(t.TempDir(), "中文 #.txt"), filepath.Join(t.TempDir(), "空文件夹")}
	array := objc.ID(objc.GetClass("NSMutableArray")).Send(objc.RegisterName("array"))
	typ := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), unsafe.StringData("public.file-url\x00"))
	for _, p := range paths {
		raw := (&url.URL{Scheme: "file", Path: p}).String()
		data := objc.ID(objc.GetClass("NSData")).Send(objc.RegisterName("dataWithBytes:length:"), unsafe.StringData(raw), uint64(len(raw)))
		item := objc.ID(objc.GetClass("NSPasteboardItem")).Send(objc.RegisterName("new"))
		if !objc.Send[bool](item, objc.RegisterName("setData:forType:"), data, typ) {
			t.Fatal("private item write failed")
		}
		array.Send(objc.RegisterName("addObject:"), item)
		item.Send(objc.RegisterName("release"))
	}
	if !objc.Send[bool](pb, objc.RegisterName("writeObjects:"), array) {
		t.Fatal("private file write failed")
	}
	got, err := readClipboardFiles()
	if err != nil || !reflect.DeepEqual(got, paths) {
		t.Fatalf("native files changed: %v %v", got, err)
	}
	if _, err := readNativePasteboardItems("public.file-url", 1, MaxFilePaths); err == nil {
		t.Fatal("unbounded native file list")
	}
	if _, err := readNativePasteboardItems("public.file-url", MaxFileListBytes, 2); err != nil {
		t.Fatal(err)
	}
	pb.Send(objc.RegisterName("clearContents"))
	if got, err := readClipboardFiles(); err != nil || len(got) != 0 {
		t.Fatal("empty file clipboard", err)
	}
}

func TestNativeFileReferenceURLReadOnPrivatePasteboard(t *testing.T) {
	pb := privateReadPasteboard(t)
	p := filepath.Join(t.TempDir(), "reference.txt")
	if err := os.WriteFile(p, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	text := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), unsafe.StringData(p+"\x00"))
	fileURL := objc.ID(objc.GetClass("NSURL")).Send(objc.RegisterName("fileURLWithPath:"), text)
	ref := fileURL.Send(objc.RegisterName("fileReferenceURL")).Send(objc.RegisterName("absoluteString"))
	typ := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), unsafe.StringData("public.file-url\x00"))
	if !objc.Send[bool](pb, objc.RegisterName("setString:forType:"), ref, typ) {
		t.Fatal("private reference write failed")
	}
	paths, err := readClipboardFiles()
	want, _ := filepath.EvalSymlinks(p)
	if err != nil || len(paths) != 1 || paths[0] != want {
		t.Fatalf("reference not resolved: %v %v", paths, err)
	}
}
