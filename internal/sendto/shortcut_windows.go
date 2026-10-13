// Package sendto registers a per-user Windows SendTo shortcut.
package sendto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const shortcutName = "Snapover 发送文件.lnk"

var (
	ole32            = windows.NewLazySystemDLL("ole32.dll")
	coInitializeEx   = ole32.NewProc("CoInitializeEx")
	coCreateInstance = ole32.NewProc("CoCreateInstance")
	clsidShellLink   = windows.GUID{Data1: 0x00021401, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidShellLinkW    = windows.GUID{Data1: 0x000214f9, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidPersistFile   = windows.GUID{Data1: 0x0000010b, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
)

// Method order follows IShellLinkW and IPersistFile in the Windows SDK.
type shellLink struct{ vtable *shellLinkVTable }
type shellLinkVTable struct {
	QueryInterface, AddRef, Release          uintptr
	GetPath, GetIDList, SetIDList            uintptr
	GetDescription, SetDescription           uintptr
	GetWorkingDirectory, SetWorkingDirectory uintptr
	GetArguments, SetArguments               uintptr
	GetHotkey, SetHotkey                     uintptr
	GetShowCmd, SetShowCmd                   uintptr
	GetIconLocation, SetIconLocation         uintptr
	SetRelativePath, Resolve, SetPath        uintptr
}

type persistFile struct{ vtable *persistFileVTable }
type persistFileVTable struct {
	QueryInterface, AddRef, Release                            uintptr
	GetClassID, IsDirty, Load, Save, SaveCompleted, GetCurFile uintptr
}

func shortcutPath() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_SendTo, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, shortcutName), nil
}

func Install(ctx context.Context) error {
	path, err := shortcutPath()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return createShortcut(ctx, path, exe)
}

func Remove() error {
	path, err := shortcutPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func createShortcut(ctx context.Context, path, exe string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	linkPath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// COM initialization, use and release must stay on the same OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitializeEx.Call(0, windows.COINIT_APARTMENTTHREADED)
	if err := shortcutError("CoInitializeEx", hr); err != nil {
		return err
	}
	defer windows.CoUninitialize()

	var link *shellLink
	hr, _, _ = coCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, 1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&link)))
	if err := shortcutError("CoCreateInstance", hr); err != nil {
		return err
	}
	defer syscall.SyscallN(link.vtable.Release, uintptr(unsafe.Pointer(link)))
	for _, field := range []struct {
		method      uintptr
		name, value string
	}{
		{link.vtable.SetPath, "SetPath", exe},
		{link.vtable.SetArguments, "SetArguments", "--send-file"},
		{link.vtable.SetWorkingDirectory, "SetWorkingDirectory", filepath.Dir(exe)},
		{link.vtable.SetDescription, "SetDescription", "Send a file with Snapover"},
	} {
		value, err := windows.UTF16PtrFromString(field.value)
		if err != nil {
			return err
		}
		hr, _, _ = syscall.SyscallN(field.method, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(value)))
		if err := shortcutError(field.name, hr); err != nil {
			return err
		}
	}
	icon, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	hr, _, _ = syscall.SyscallN(link.vtable.SetIconLocation, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(icon)), 0)
	if err := shortcutError("SetIconLocation", hr); err != nil {
		return err
	}
	var file *persistFile
	hr, _, _ = syscall.SyscallN(link.vtable.QueryInterface, uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(&iidPersistFile)), uintptr(unsafe.Pointer(&file)))
	if err := shortcutError("QueryInterface(IPersistFile)", hr); err != nil {
		return err
	}
	defer syscall.SyscallN(file.vtable.Release, uintptr(unsafe.Pointer(file)))
	if err := ctx.Err(); err != nil {
		return err
	}
	hr, _, _ = syscall.SyscallN(file.vtable.Save, uintptr(unsafe.Pointer(file)), uintptr(unsafe.Pointer(linkPath)), 1)
	return shortcutError("IPersistFile.Save", hr)
}

func shortcutError(operation string, hr uintptr) error {
	// S_FALSE (1) also succeeds and still requires CoUninitialize.
	if int32(hr) >= 0 {
		return nil
	}
	return fmt.Errorf("创建快捷方式失败：%s (HRESULT 0x%08X)：%s", operation, uint32(hr), syscall.Errno(uint32(hr)).Error())
}
