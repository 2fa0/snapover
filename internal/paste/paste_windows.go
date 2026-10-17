//go:build windows

package paste

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	swRestore      = 9
	vkControl      = 0x11
	vkV            = 0x56
	keyeventfKeyup = 0x0002
)

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	procForeground    = user32.NewProc("GetForegroundWindow")
	procSetForeground = user32.NewProc("SetForegroundWindow")
	procThreadID      = user32.NewProc("GetWindowThreadProcessId")
	procAttach        = user32.NewProc("AttachThreadInput")
	procIsIconic      = user32.NewProc("IsIconic")
	procShow          = user32.NewProc("ShowWindow")
	procAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	procKey           = user32.NewProc("keybd_event")
)

func Capture() Target {
	hwnd, _, _ := procForeground.Call()
	if hwnd == 0 || windowPID(hwnd) == os.Getpid() {
		return Target{}
	}
	return Target{id: hwnd}
}

func Allowed() bool { return true }

func Prompt() {}

func (t Target) Activate() error {
	if !t.Valid() {
		return ErrNoTarget
	}
	if !focus(t.id) {
		return ErrActivate
	}
	return nil
}

func (t Target) SendPaste() error {
	if !t.Valid() || Capture() != t {
		return ErrActivate
	}
	tap(vkControl, 0)
	tap(vkV, 0)
	tap(vkV, keyeventfKeyup)
	tap(vkControl, keyeventfKeyup)
	return nil
}

func focus(hwnd uintptr) bool {
	// SW_RESTORE also unmaximizes windows, so only use it for minimized targets.
	if minimized, _, _ := procIsIconic.Call(hwnd); minimized != 0 {
		procShow.Call(hwnd, swRestore)
	}
	ours := uintptr(windows.GetCurrentThreadId())
	theirs, _, _ := procThreadID.Call(hwnd, 0)
	attached := false
	if theirs != 0 && theirs != ours {
		if r, _, _ := procAttach.Call(ours, theirs, 1); r != 0 {
			attached = true
			defer procAttach.Call(ours, theirs, 0)
		}
	}
	r, _, _ := procSetForeground.Call(hwnd)
	return r != 0 || attached
}

func windowPID(hwnd uintptr) int {
	var pid uint32
	procThreadID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return int(pid)
}

func tap(vk byte, flags uintptr) {
	procKey.Call(uintptr(vk), 0, flags, 0)
}

func ModifiersDown() bool {
	for _, key := range []uintptr{0x10, 0x11, 0x12, 0x5b, 0x5c} {
		state, _, _ := procAsyncKeyState.Call(key)
		if state&0x8000 != 0 {
			return true
		}
	}
	return false
}

func (t Target) SendCopy() error {
	if !t.Valid() || Capture() != t {
		return ErrActivate
	}
	tap(vkControl, 0)
	tap(0x43, 0)
	tap(0x43, keyeventfKeyup)
	tap(vkControl, keyeventfKeyup)
	return nil
}
