//go:build windows

package paste

import (
	"os"
	"time"
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
	procShow          = user32.NewProc("ShowWindow")
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

func (t Target) Paste() error {
	if !t.Valid() {
		return ErrNoTarget
	}
	if !focus(t.id) {
		return ErrActivate
	}
	time.Sleep(60 * time.Millisecond)
	tap(vkControl, 0)
	tap(vkV, 0)
	tap(vkV, keyeventfKeyup)
	tap(vkControl, keyeventfKeyup)
	return nil
}

func focus(hwnd uintptr) bool {
	procShow.Call(hwnd, swRestore)
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
