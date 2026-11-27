package app

import (
	"sync"
	"unsafe"

	"github.com/tangoui/kkdesk/internal/session"
	"golang.org/x/sys/windows"
)

const windowsCursorShowing = 1

type windowsCursorInfo struct {
	size, flags uint32
	handle      uintptr
	x, y        int32
}

var (
	cursorUser32      = windows.NewLazySystemDLL("user32.dll")
	procGetCursorInfo = cursorUser32.NewProc("GetCursorInfo")
	procLoadCursorW   = cursorUser32.NewProc("LoadCursorW")
	windowsCursors    = sync.OnceValue(func() map[uintptr]session.CursorShape {
		return windowsCursorHandles(func(id uintptr) uintptr {
			handle, _, _ := procLoadCursorW.Call(0, id)
			return handle
		})
	})
)

func windowsCursorHandles(load func(uintptr) uintptr) map[uintptr]session.CursorShape {
	handles := make(map[uintptr]session.CursorShape)
	for _, cursor := range []struct {
		id    uintptr
		shape session.CursorShape
	}{
		{32512, session.CursorDefault},    // IDC_ARROW
		{32649, session.CursorPointer},    // IDC_HAND
		{32513, session.CursorText},       // IDC_IBEAM
		{32648, session.CursorNotAllowed}, // IDC_NO
		{32644, session.CursorResizeEW},   // IDC_SIZEWE
		{32645, session.CursorResizeNS},   // IDC_SIZENS
		{32646, session.CursorMove},       // IDC_SIZEALL
	} {
		if handle := load(cursor.id); handle != 0 {
			handles[handle] = cursor.shape
		}
	}
	return handles
}

func windowsCursorShape(flags uint32, handle uintptr, handles map[uintptr]session.CursorShape) session.CursorShape {
	if flags&windowsCursorShowing == 0 {
		return session.CursorDefault
	}
	return handles[handle] // Unrecognized handles map to the arrow.
}

func hostCursor() (session.CursorShape, bool) {
	var info windowsCursorInfo
	info.size = uint32(unsafe.Sizeof(info))
	ok, _, _ := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return session.CursorDefault, false
	}
	return windowsCursorShape(info.flags, info.handle, windowsCursors()), true
}
