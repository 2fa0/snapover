//go:build windows

package keepawake

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const Supported = true

var (
	user32             = windows.NewLazySystemDLL("user32.dll")
	kernel32           = windows.NewLazySystemDLL("kernel32.dll")
	wtsapi32           = windows.NewLazySystemDLL("wtsapi32.dll")
	procParameters     = user32.NewProc("SystemParametersInfoW")
	procLastInput      = user32.NewProc("GetLastInputInfo")
	procSendInput      = user32.NewProc("SendInput")
	procThreadDesktop  = user32.NewProc("GetThreadDesktop")
	procObjectInfo     = user32.NewProc("GetUserObjectInformationW")
	procSessionInfo    = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procExecutionState = kernel32.NewProc("SetThreadExecutionState")
	procTickCount      = kernel32.NewProc("GetTickCount")
)

type windowsBackend struct{}

func platformBackend() (backend, error) { return windowsBackend{}, nil }

func (windowsBackend) check() error {
	var blocked int32
	r, _, err := procParameters.Call(0x1026, 0, uintptr(unsafe.Pointer(&blocked)), 0) // SPI_GETBLOCKSENDINPUTRESETS
	if r == 0 {
		return apiError("检查屏保限制失败", err)
	}
	if blocked != 0 {
		return errors.New("系统禁止模拟输入重置屏保计时，请联系管理员调整")
	}
	return nil
}

func (windowsBackend) interactive() (bool, error) {
	active, err := activeSession(wtsSessionActive, consoleSessionActive)
	if err != nil || !active {
		return false, err
	}
	desktop, _, err := procThreadDesktop.Call(uintptr(windows.GetCurrentThreadId()))
	if desktop == 0 {
		return false, apiError("检查当前桌面失败", err)
	}
	// UOI_IO is false when Winlogon, the screen saver or a secure prompt
	// owns input. Never switch desktops or inject into those contexts.
	var receivesInput int32
	r, _, err := procObjectInfo.Call(desktop, 6, uintptr(unsafe.Pointer(&receivesInput)), unsafe.Sizeof(receivesInput), 0)
	if r == 0 {
		return false, apiError("检查交互桌面失败", err)
	}
	return receivesInput != 0, nil
}

func wtsSessionActive() (bool, error) {
	var state *uint32
	var size uint32
	r, _, err := procSessionInfo.Call(0, 0xffffffff, 8, uintptr(unsafe.Pointer(&state)), uintptr(unsafe.Pointer(&size))) // WTS_CURRENT_SESSION, WTSConnectState
	if r == 0 {
		if sessionServiceUnavailable(err) {
			return false, fmt.Errorf("%w：%v", errSessionUnavailable, err)
		}
		return false, apiError("检查当前会话失败", err)
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(state)))
	if state == nil || size < 4 {
		return false, errors.New("当前会话状态无效")
	}
	return *state == windows.WTSActive, nil
}

func sessionServiceUnavailable(err error) bool {
	return errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) ||
		errors.Is(err, windows.RPC_S_INVALID_BINDING) ||
		errors.Is(err, windows.RPC_S_UNKNOWN_IF) ||
		errors.Is(err, windows.RPC_S_SERVER_UNAVAILABLE) ||
		errors.Is(err, windows.EPT_S_NOT_REGISTERED)
}

func consoleSessionActive() (bool, error) {
	console := windows.WTSGetActiveConsoleSessionId()
	if console == 0xffffffff {
		return false, nil
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		return false, apiError("检查本地控制台会话失败", err)
	}
	return session == console, nil
}

func (windowsBackend) power(on bool) error {
	flags := uintptr(0x80000000) // ES_CONTINUOUS
	if on {
		flags |= 0x1 | 0x2 // ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED
	}
	r, _, err := procExecutionState.Call(flags)
	if r == 0 {
		return apiError("更新保持唤醒状态失败", err)
	}
	return nil
}

func (windowsBackend) idle() (time.Duration, error) {
	info := struct{ Size, Time uint32 }{Size: 8}
	r, _, err := procLastInput.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, apiError("读取空闲时间失败", err)
	}
	now, _, _ := procTickCount.Call()
	// Both timestamps wrap at 32 bits; subtraction preserves elapsed time.
	return time.Duration(uint32(now)-info.Time) * time.Millisecond, nil
}

// Native alignment yields sizeof(INPUT)=40 on amd64/arm64 and 28 on 386.
type mouseInput struct {
	DX, DY    int32
	Data      uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type input struct {
	Type  uint32
	Mouse mouseInput
}

func (b windowsBackend) input() error {
	// Check again immediately before injection to narrow desktop-switch races.
	active, err := b.interactive()
	if err != nil || !active {
		return err
	}
	event := input{Mouse: mouseInput{Flags: 0x1}} // MOUSEEVENTF_MOVE, zero relative displacement
	r, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&event)), unsafe.Sizeof(event))
	if r != 1 {
		return apiError("模拟输入失败，可能被系统权限限制，请关闭后重试", err)
	}
	return nil
}

func apiError(message string, err error) error {
	if err == nil || err == windows.ERROR_SUCCESS {
		return errors.New(message)
	}
	return fmt.Errorf("%s：%w", message, err)
}
