package keepalive

import (
	"testing"
	"unsafe"
)

func TestInputNativeLayout(t *testing.T) {
	wantSize, wantOffset := uintptr(28), uintptr(4)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		wantSize, wantOffset = 40, 8
	}
	if unsafe.Sizeof(input{}) != wantSize || unsafe.Offsetof(input{}.Mouse) != wantOffset {
		t.Fatal("INPUT does not match the Windows ABI")
	}
}

func TestAbsoluteCoordinatesRestorePixelsAcrossMonitors(t *testing.T) {
	for _, screen := range []struct{ origin, extent int32 }{{0, 1920}, {-1920, 3840}, {0, 7680}} {
		for pixel := int32(0); pixel < screen.extent; pixel++ {
			normalized := normalize(screen.origin+pixel, screen.origin, screen.extent)
			if restored := int32(int64(normalized) * int64(screen.extent) / 65536); restored != pixel {
				t.Fatalf("pixel %d restored as %d", pixel, restored)
			}
		}
	}
}
