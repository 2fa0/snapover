package settings

import "testing"

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	f, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Values.MaxImageMB != DefaultMaxImageMB {
		t.Fatalf("default mb = %d", f.Values.MaxImageMB)
	}
	f.Values.Paused = true
	f.Values.TextOnly = true
	f.Values.MaxImageMB = 8
	f.Values.ReceiveDir = dir
	f.Values.DisableSendTo = true
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Values.Paused || !again.Values.TextOnly || again.Values.MaxImageMB != 8 || again.Values.ReceiveDir != dir || !again.Values.DisableSendTo {
		t.Fatalf("%+v", again.Values)
	}
}

func TestClampImageLimit(t *testing.T) {
	if clampMB(0) != DefaultMaxImageMB || clampMB(100) != DefaultMaxImageMB || clampMB(3) != 3 {
		t.Fatal("clamp")
	}
}
