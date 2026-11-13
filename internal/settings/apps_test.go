package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The setting held names, one per string, before it had app identifiers: an
// older settings.json must keep its rules.
func TestExcludedAppsReadsTheNamesOfTheOlderSetting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"excluded_apps":[" Safari ","1Password","Safari"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Values.ExcludedApps) != 2 {
		t.Fatalf("entries = %+v", f.Values.ExcludedApps)
	}
	if first := f.Values.ExcludedApps[0]; first.Name != "Safari" || first.ID != "" || first.Off {
		t.Fatalf("first entry = %+v", first)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf), `"name": "1Password"`) {
		t.Fatalf("saved entry is not an object: %s", buf)
	}
	again, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Values.ExcludedApps) != 2 || again.Values.ExcludedApps[1].Name != "1Password" {
		t.Fatalf("entries after save = %+v", again.Values.ExcludedApps)
	}
}

func TestExcludedAppsActiveSkipsSwitchedOffRows(t *testing.T) {
	list := ExcludedApps{{Name: "Safari", ID: "com.apple.Safari"}, {Name: "1Password", Off: true}, {Name: "  "}}
	active := list.Active()
	if len(active) != 1 || active[0].Name != "Safari" {
		t.Fatalf("active = %+v", active)
	}
	if list.Find(ExcludedApp{ID: "com.apple.safari"}) != 0 {
		t.Fatal("identifier matched case-insensitively")
	}
	if list.Find(ExcludedApp{Name: "Brave Browser"}) != -1 {
		t.Fatal("unknown app found")
	}
	got := ExcludedApps{{Name: " Safari "}, {Name: "safari"}}.Clean()
	if len(got) != 1 || got[0].Name != "Safari" {
		t.Fatalf("clean = %+v", got)
	}
}
