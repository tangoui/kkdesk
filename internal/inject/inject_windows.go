//go:build windows

package inject

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"mmdesk/internal/input"
)

// The Windows backend calls user32 directly (no cgo).
//
// Limits (UIPI): the process runs with normal rights, so SendInput cannot
// reach windows of elevated (administrator) processes, and nothing reaches
// the secure desktop (UAC prompts, Ctrl+Alt+Del, the lock screen). Windows
// drops such input silently: neither the return value nor GetLastError
// reports a UIPI block.
//
// DPI: making the process per-monitor DPI aware
// (SetProcessDpiAwarenessContext(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2))
// is the application's job, done once at startup. Pointer positions do not
// depend on it, since MOUSEEVENTF_ABSOLUTE coordinates are normalized to the
// primary monitor; only Bounds (GetSystemMetrics) reports physical pixels
// when the process is DPI aware and scaled pixels when it is not.
//
// Multiple monitors: positions are on the primary monitor only (absolute
// coordinates without MOUSEEVENTF_VIRTUALDESK), matching the captured
// display.
var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procSendInput        = user32.NewProc("SendInput")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	smCXScreen = 0
	smCYScreen = 1
)

// HasPermission always reports true: SendInput needs no permission, though
// UIPI limits what it can reach (see above).
func HasPermission() bool { return true }

// RequestPermission does nothing on Windows.
func RequestPermission() {}

// Injector sends input.Events to the local desktop with SendInput.
// Normalized coordinates refer to the primary monitor. It is safe for
// concurrent use; events are sent in Apply call order.
type Injector struct {
	mu     sync.Mutex
	bounds Rect
	tr     winTranslator
}

// New returns an Injector for the primary monitor.
func New() (*Injector, error) {
	if unsafe.Sizeof(winInput{}) != 40 {
		return nil, ErrUnsupported // INPUT layout is for 64-bit Windows only
	}
	if err := procSendInput.Find(); err != nil {
		return nil, fmt.Errorf("inject: %w", err)
	}
	in := &Injector{}
	in.tr.pxPerNotch = WheelPixelsPerNotch
	in.Refresh()
	if in.bounds.W <= 0 || in.bounds.H <= 0 {
		return nil, errors.New("inject: primary monitor has no size")
	}
	return in, nil
}

// Refresh re-reads the primary monitor size (GetSystemMetrics
// SM_CXSCREEN/SM_CYSCREEN). Apply does it for every pointer event.
func (in *Injector) Refresh() {
	in.mu.Lock()
	in.refresh()
	in.mu.Unlock()
}

func (in *Injector) refresh() {
	w, _, _ := procGetSystemMetrics.Call(smCXScreen)
	h, _, _ := procGetSystemMetrics.Call(smCYScreen)
	in.tr.w, in.tr.h = int32(w), int32(h)
	in.bounds = Rect{W: float64(int32(w)), H: float64(int32(h))}
}

// Bounds returns the cached primary monitor rectangle: origin (0, 0), size in
// pixels of the process's DPI awareness (see the package notes above).
func (in *Injector) Bounds() Rect {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.bounds
}

// Apply sends one event (see winTranslator.inputs for the mapping). Unknown
// kinds, unsupported buttons and unmapped HID usages return an error and
// send nothing.
func (in *Injector) Apply(e input.Event) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if e.Kind == input.Move || e.Kind == input.Button {
		in.refresh()
	}
	ins, err := in.tr.inputs(e)
	if err != nil || len(ins) == 0 {
		return err
	}
	n, _, callErr := procSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
	if int(n) != len(ins) {
		return fmt.Errorf("inject: SendInput sent %d of %d events: %v", n, len(ins), callErr)
	}
	return nil
}
