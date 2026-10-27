package filetransfer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

func TestBundleDirectoryBoundsBeforeZIPAllocation(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for i := 0; i < MaxBundleEntries+1; i++ {
		if _, err := w.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("f%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	if err := checkBundleDirectory(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("excessive directory accepted")
	}
	// Lying about the count must not make the ZIP parser scan extra entries.
	binary.LittleEndian.PutUint16(raw[len(raw)-14:len(raw)-12], 1)
	binary.LittleEndian.PutUint16(raw[len(raw)-12:len(raw)-10], 1)
	if err := checkBundleDirectory(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("forged directory count accepted")
	}
}

func TestBundleZIP64Directory(t *testing.T) {
	name := makeBundle(t, []string{"file"}, zip.Store)
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	end := append([]byte(nil), raw[len(raw)-22:]...)
	offset := uint64(binary.LittleEndian.Uint32(end[16:20]))
	length := uint64(binary.LittleEndian.Uint32(end[12:16]))
	large := make([]byte, 56)
	binary.LittleEndian.PutUint32(large[:4], 0x06064b50)
	binary.LittleEndian.PutUint64(large[4:12], 44)
	binary.LittleEndian.PutUint16(large[12:14], 45)
	binary.LittleEndian.PutUint16(large[14:16], 45)
	binary.LittleEndian.PutUint64(large[24:32], 1)
	binary.LittleEndian.PutUint64(large[32:40], 1)
	binary.LittleEndian.PutUint64(large[40:48], length)
	binary.LittleEndian.PutUint64(large[48:56], offset)
	locator := make([]byte, 20)
	binary.LittleEndian.PutUint32(locator[:4], 0x07064b50)
	binary.LittleEndian.PutUint64(locator[8:16], uint64(len(raw)-22))
	binary.LittleEndian.PutUint32(locator[16:20], 1)
	binary.LittleEndian.PutUint16(end[8:10], 0xffff)
	binary.LittleEndian.PutUint16(end[10:12], 0xffff)
	binary.LittleEndian.PutUint32(end[12:16], 0xffffffff)
	binary.LittleEndian.PutUint32(end[16:20], 0xffffffff)
	raw = append(raw[:len(raw)-22], large...)
	raw = append(raw, locator...)
	raw = append(raw, end...)
	if err := checkBundleDirectory(bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := UnpackBundle(context.Background(), name, t.TempDir(), 100); err != nil {
		t.Fatal(err)
	}
}
