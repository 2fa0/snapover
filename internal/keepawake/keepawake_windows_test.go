package keepawake

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNativeInputLayout(t *testing.T) {
	wantSize, wantOffset := uintptr(40), uintptr(8)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		wantSize, wantOffset = 28, 4
	}
	if unsafe.Sizeof(input{}) != wantSize || unsafe.Offsetof(input{}.Mouse) != wantOffset {
		t.Fatalf("INPUT layout: size=%d offset=%d", unsafe.Sizeof(input{}), unsafe.Offsetof(input{}.Mouse))
	}
}

func TestSessionServiceUnavailableErrors(t *testing.T) {
	for _, err := range []error{windows.ERROR_SERVICE_NOT_ACTIVE, windows.RPC_S_INVALID_BINDING, windows.RPC_S_UNKNOWN_IF, windows.RPC_S_SERVER_UNAVAILABLE, windows.EPT_S_NOT_REGISTERED} {
		if !sessionServiceUnavailable(err) {
			t.Fatalf("service error not recognized: %v", err)
		}
	}
	for _, err := range []error{nil, windows.ERROR_SUCCESS, windows.ERROR_ACCESS_DENIED, windows.ERROR_INVALID_PARAMETER} {
		if sessionServiceUnavailable(err) {
			t.Fatalf("unrelated error allowed console fallback: %v", err)
		}
	}
}
