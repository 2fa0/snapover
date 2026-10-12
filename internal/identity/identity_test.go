package identity

import (
	"os"
	"testing"
)

func TestLoadOrCreateStable(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.DeviceID) != 32 || len(a.Fingerprint) != 64 {
		t.Fatalf("id %q fp %q", a.DeviceID, a.Fingerprint)
	}
	info, err := os.Stat(dir + "/identity/key.pem")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %o", info.Mode().Perm())
	}
	b, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.DeviceID != b.DeviceID || a.Fingerprint != b.Fingerprint {
		t.Fatalf("identity changed: %s -> %s", a, b)
	}
}
