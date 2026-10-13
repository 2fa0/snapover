//go:build windows

package paste

import "unsafe"

type caretRect struct{ Left, Top, Right, Bottom int32 }

type guiThreadInfo struct {
	Size, Flags                                        uint32
	Active, Focus, Capture, MenuOwner, MoveSize, Caret uintptr
	Bounds                                             caretRect
}

var (
	procGUIThreadInfo  = user32.NewProc("GetGUIThreadInfo")
	procClientToScreen = user32.NewProc("ClientToScreen")
)

// CaretBounds reads the target's system caret before the picker takes focus.
// Windows screen coordinates are physical pixels. Custom carets may be unavailable.
func (t Target) CaretBounds() (Rectangle, bool) {
	if !t.Valid() {
		return Rectangle{}, false
	}
	thread, _, _ := procThreadID.Call(t.id, 0)
	if thread == 0 {
		return Rectangle{}, false
	}
	info := guiThreadInfo{Size: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	if ok, _, _ := procGUIThreadInfo.Call(thread, uintptr(unsafe.Pointer(&info))); ok == 0 || info.Caret == 0 {
		return Rectangle{}, false
	}
	r := info.Bounds
	if r.Bottom <= r.Top || r.Right < r.Left {
		return Rectangle{}, false
	}
	points := [2]struct{ X, Y int32 }{{r.Left, r.Top}, {r.Right, r.Bottom}}
	for i := range points {
		if ok, _, _ := procClientToScreen.Call(info.Caret, uintptr(unsafe.Pointer(&points[i]))); ok == 0 {
			return Rectangle{}, false
		}
	}
	return Rectangle{
		X: int(points[0].X), Y: int(points[0].Y),
		Width: int(points[1].X - points[0].X), Height: int(points[1].Y - points[0].Y),
	}, true
}
