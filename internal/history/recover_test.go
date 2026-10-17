package history

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
)

func TestUnavailableHistoryDoesNotPretendToSave(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "index.json")
	if err := os.WriteFile(marker, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("corrupt history opened")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("failed open removed the damaged file", err)
	}
	broken := Unavailable(dir, errors.New("历史日志校验失败"))
	text, _ := clip.FromText("不能假装保存")
	if _, err := broken.Add(DirLocal, "", text); err == nil || !strings.Contains(err.Error(), "历史不可用") {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	moved, err := Quarantine(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "index.json")); err != nil {
		t.Fatal("damaged history was not preserved", err)
	}
	fresh, err := Open(dir)
	if err != nil || fresh.Disabled() != nil || len(fresh.List()) != 0 {
		t.Fatal(err, fresh)
	}
}
