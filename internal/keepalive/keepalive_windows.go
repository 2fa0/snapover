package keepalive

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const Supported = true

var (
	user32        = windows.NewLazySystemDLL("user32.dll")
	querySession  = windows.NewLazySystemDLL("wtsapi32.dll").NewProc("WTSQuerySessionInformationW")
	getLastInput  = user32.NewProc("GetLastInputInfo")
	getTickCount  = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount")
	openDesktop   = user32.NewProc("OpenInputDesktop")
	closeDesktop  = user32.NewProc("CloseDesktop")
	getObjectInfo = user32.NewProc("GetUserObjectInformationW")
	getKeyState   = user32.NewProc("GetAsyncKeyState")
	getCursorPos  = user32.NewProc("GetCursorPos")
	getMetrics    = user32.NewProc("GetSystemMetrics")
	sendInput     = user32.NewProc("SendInput")
)

func currentState() (sessionState, error) {
	var s sessionState
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		return s, err
	}
	s.sessionID = session
	protocol, err := sessionValue(session, 16, 2) // WTSClientProtocolType: 2 is RDP.
	if err != nil {
		return s, err
	}
	s.protocol = protocol
	s.remote = protocol == 2
	if !s.remote {
		return s, nil
	}
	state, err := sessionValue(session, 8, 4) // WTSConnectState.
	if err != nil {
		return s, err
	}
	s.connected = state == windows.WTSActive
	if !s.connected {
		return s, nil
	}
	s.desktop, err = inputDesktopName()
	if err != nil {
		return s, err
	}
	s.unlocked = s.desktop == "Default"
	if !s.unlocked {
		return s, nil
	}
	last := struct{ Size, Tick uint32 }{Size: 8}
	if ok, _, err := getLastInput.Call(uintptr(unsafe.Pointer(&last))); ok == 0 {
		return s, fmt.Errorf("GetLastInputInfo: %w", err)
	}
	now, _, _ := getTickCount.Call()
	// Both counters are DWORD milliseconds; subtraction handles their wraparound.
	s.idle = time.Duration(uint32(now)-last.Tick) * time.Millisecond
	// WTS and GetLastInputInfo expose separate observations. Keep the WTS query
	// diagnostic-only so its failure does not stop the existing input behavior.
	s.rdpLastInput, s.rdpIdle, s.rdpError = sessionInputTime(session)
	for _, key := range []uintptr{0x01, 0x02, 0x04, 0x05, 0x06, 0x10, 0x11, 0x12, 0x5B, 0x5C} {
		if down, _, _ := getKeyState.Call(key); down&0x8000 != 0 {
			s.held = true
			break
		}
	}
	return s, nil
}

func sessionValue(session, kind, size uint32) (uint32, error) {
	var buffer *byte
	var length uint32
	ok, _, err := querySession.Call(0, uintptr(session), uintptr(kind), uintptr(unsafe.Pointer(&buffer)), uintptr(unsafe.Pointer(&length)))
	if ok == 0 {
		return 0, fmt.Errorf("WTSQuerySessionInformation: %w", err)
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buffer)))
	if buffer == nil || length < size {
		return 0, fmt.Errorf("WTSQuerySessionInformation: incomplete response")
	}
	if size == 2 {
		return uint32(*(*uint16)(unsafe.Pointer(buffer))), nil
	}
	return *(*uint32)(unsafe.Pointer(buffer)), nil
}

// WTSINFOW uses 8-byte alignment for LARGE_INTEGER on Windows, including x86.
type sessionInfo struct {
	Counters       [8]uint32
	Station        [32]uint16
	Domain         [17]uint16
	User           [21]uint16
	_              uint32
	ConnectTime    int64
	DisconnectTime int64
	LastInputTime  int64
	LogonTime      int64
	CurrentTime    int64
}

func sessionInputTime(session uint32) (int64, time.Duration, error) {
	var buffer *sessionInfo
	var length uint32
	const wtsSessionInfo = 24
	ok, _, err := querySession.Call(0, uintptr(session), wtsSessionInfo, uintptr(unsafe.Pointer(&buffer)), uintptr(unsafe.Pointer(&length)))
	if ok == 0 {
		return 0, 0, fmt.Errorf("WTSQuerySessionInformation(WTSSessionInfo): %w", err)
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buffer)))
	if buffer == nil || uintptr(length) < unsafe.Sizeof(sessionInfo{}) {
		return 0, 0, fmt.Errorf("WTSQuerySessionInformation(WTSSessionInfo): incomplete response")
	}
	if buffer.LastInputTime <= 0 || buffer.CurrentTime < buffer.LastInputTime {
		return 0, 0, fmt.Errorf("WTSQuerySessionInformation(WTSSessionInfo): invalid input time")
	}
	return buffer.LastInputTime, time.Duration(buffer.CurrentTime-buffer.LastInputTime) * 100 * time.Nanosecond, nil
}

func inputDesktopName() (string, error) {
	const desktopReadObjects = 1
	desktop, _, err := openDesktop.Call(0, 0, desktopReadObjects)
	if desktop == 0 {
		return "", fmt.Errorf("OpenInputDesktop: %w", err)
	}
	defer closeDesktop.Call(desktop)
	var name [256]uint16
	var needed uint32
	const objectName = 2
	ok, _, err := getObjectInfo.Call(desktop, objectName, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Sizeof(name)), uintptr(unsafe.Pointer(&needed)))
	if ok == 0 {
		return "", fmt.Errorf("GetUserObjectInformationW: %w", err)
	}
	// Lock, sign-in and UAC desktops must never receive the generated input.
	return windows.UTF16ToString(name[:]), nil
}

type mouseInput struct {
	X, Y              int32
	Data, Flags, Time uint32
	ExtraInfo         uintptr
}

// The mouse member is the largest INPUT union member. Its native alignment
// provides the padding after Type on 64-bit Windows (40 bytes, versus 28 on 32-bit).
type input struct {
	Type  uint32
	Mouse mouseInput
}

func moveMouse() error {
	var pos struct{ X, Y int32 }
	if ok, _, err := getCursorPos.Call(uintptr(unsafe.Pointer(&pos))); ok == 0 {
		return fmt.Errorf("GetCursorPos: %w", err)
	}
	metric := func(index uintptr) int32 {
		value, _, _ := getMetrics.Call(index)
		return int32(value)
	}
	x, y, width, height := metric(76), metric(77), metric(78), metric(79)
	if width <= 1 || height <= 1 {
		return fmt.Errorf("GetSystemMetrics: invalid virtual screen")
	}
	nextX := pos.X + 1
	if nextX >= x+width {
		nextX = pos.X - 1
	}
	const moveAbsolute = 0x0001 | 0x2000 | 0x4000 | 0x8000
	events := [2]input{
		{Mouse: mouseInput{X: normalize(nextX, x, width), Y: normalize(pos.Y, y, height), Flags: moveAbsolute}},
		{Mouse: mouseInput{X: normalize(pos.X, x, width), Y: normalize(pos.Y, y, height), Flags: moveAbsolute}},
	}
	// A single batch prevents physical input being interleaved with the return
	// movement. Absolute coordinates avoid mouse acceleration and edge drift.
	sent, _, err := sendInput.Call(2, uintptr(unsafe.Pointer(&events[0])), unsafe.Sizeof(events[0]))
	if sent != 2 {
		return fmt.Errorf("SendInput: inserted %d of 2 events (%v)", sent, err)
	}
	return nil
}

func normalize(position, origin, extent int32) int32 {
	// Aim at the pixel center in the 0..65535 absolute coordinate space.
	value := ((int64(position)-int64(origin))*2 + 1) * 65536 / (int64(extent) * 2)
	if value < 0 {
		return 0
	}
	if value > 65535 {
		return 65535
	}
	return int32(value)
}
