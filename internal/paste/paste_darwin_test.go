//go:build darwin

package paste

import "testing"

func TestCaptureAndKeyEventDoNotPanic(t *testing.T) {
	_ = Capture()
	if !Allowed() && axErr != nil {
		t.Fatal(axErr)
	}
	loadCG()
	if cgErr != nil {
		t.Fatal(cgErr)
	}
	ev := cgCreate(0, kVK_ANSI_V, 1)
	if ev == 0 {
		t.Fatal("keyboard event was not created")
	}
	cfRelease(ev)
}
