//go:build windows && cgo

package display

import (
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/tangoui/kkdesk/internal/winmedia"
)

var procSetWindowPos = syscall.NewLazyDLL("user32.dll").NewProc("SetWindowPos")

// resize sets the view window's size; WM_SIZE is delivered synchronously
// because the test thread owns the window.
func resize(t *testing.T, hwnd uintptr, w, h int) {
	t.Helper()
	const swpNoMove, swpNoZOrder, swpNoActivate = 0x0002, 0x0004, 0x0010
	if r, _, err := procSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(w), uintptr(h), swpNoMove|swpNoZOrder|swpNoActivate); r == 0 {
		t.Fatalf("SetWindowPos: %v", err)
	}
}

// newTestView creates a view on a locked OS thread (the window's thread).
func newTestView(t *testing.T) *View {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	v, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(v.Release)
	return v
}

func solid(t *testing.T, w, h int, y uint8) uintptr {
	t.Helper()
	tex, err := newSolidNV12(w, h, y, 128, 128)
	if err != nil {
		t.Fatalf("newSolidNV12: %v", err)
	}
	t.Cleanup(func() { winmedia.Release(tex) })
	return tex
}

func TestEnqueueFrameSize(t *testing.T) {
	v := newTestView(t)
	if v.Handle() == 0 {
		t.Fatal("Handle() == 0")
	}
	resize(t, v.Handle(), 640, 360)
	before := time.Now()
	at, err := v.Enqueue(solid(t, 1280, 720, 128), before)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if at.Before(before) || at.After(time.Now()) {
		t.Errorf("enqueuedAt %v outside the call", at)
	}
	if w, h := v.FrameSize(); w != 1280 || h != 720 {
		t.Errorf("FrameSize = %dx%d, want 1280x720", w, h)
	}
	if _, err := v.Enqueue(0, time.Now()); err == nil {
		t.Error("Enqueue(0) returned nil")
	}
}

func TestLetterbox(t *testing.T) {
	v := newTestView(t)
	resize(t, v.Handle(), 800, 800)
	if _, err := v.Enqueue(solid(t, 1600, 900, 235), time.Now()); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	target, err := newBGRA(800, 800)
	if err != nil {
		t.Fatalf("newBGRA: %v", err)
	}
	defer winmedia.Release(target)
	if err := v.renderTo(target); err != nil {
		t.Fatalf("renderTo: %v", err)
	}
	// VideoRect(800, 800, 1600, 900) = (0, 175, 800, 450).
	if r, g, b, err := pixelAt(target, 400, 100); err != nil || r > 8 || g > 8 || b > 8 {
		t.Errorf("bar pixel (400,100) = %d,%d,%d (%v), want black", r, g, b, err)
	}
	if r, g, b, err := pixelAt(target, 400, 400); err != nil || r < 240 || g < 240 || b < 240 {
		t.Errorf("video pixel (400,400) = %d,%d,%d (%v), want white", r, g, b, err)
	}
}

func TestRepaintOnResize(t *testing.T) {
	v := newTestView(t)
	resize(t, v.Handle(), 640, 360)
	if _, err := v.Enqueue(solid(t, 1280, 720, 128), time.Now()); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	n := v.presents()
	if n == 0 {
		t.Fatal("Enqueue did not present")
	}
	resize(t, v.Handle(), 900, 500)
	if got := v.presents(); got <= n {
		t.Fatalf("presents = %d after resize, want > %d (repaint without a new frame)", got, n)
	}
}

func TestNextVsyncWindows(t *testing.T) {
	v := newTestView(t)
	after := time.Now()
	got := v.NextVsync(after)
	if got.Before(after) || got.Sub(after) > 50*time.Millisecond {
		t.Fatalf("NextVsync(%v) = %v (+%v), want within [0, 50ms]", after, got, got.Sub(after))
	}
}

func TestReleaseIdempotentWindows(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	v, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	v.Release()
	v.Release()
	if v.Handle() != 0 {
		t.Error("Handle() != 0 after Release")
	}
	tex := solid(t, 64, 64, 128)
	if _, err := v.Enqueue(tex, time.Now()); err == nil {
		t.Error("Enqueue after Release returned nil")
	}
}

func TestEnqueueDeviceWithoutVideoSupport(t *testing.T) {
	v := newTestView(t)
	resize(t, v.Handle(), 320, 180)
	tex, err := newNoVideoNV12(640, 360)
	if err != nil {
		t.Skipf("cannot create a device without video support: %v", err)
	}
	defer winmedia.Release(tex)
	for i := 0; i < 3; i++ {
		if _, err := v.Enqueue(tex, time.Now()); err == nil {
			t.Fatalf("Enqueue %d on a device without video support returned nil", i)
		}
	}
}

var (
	procPeekMessageW     = syscall.NewLazyDLL("user32.dll").NewProc("PeekMessageW")
	procDispatchMessageW = syscall.NewLazyDLL("user32.dll").NewProc("DispatchMessageW")
)

// pump dispatches this thread's messages for d.
func pump(d time.Duration) {
	var msg [48]byte // MSG
	for end := time.Now().Add(d); time.Now().Before(end); {
		for {
			r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0, 1) // PM_REMOVE
			if r == 0 {
				break
			}
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg[0])))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// busy holds the view's C lock on another OS thread (like a frame being
// presented by the decoder) until the returned function is called.
func busy(v *View) (done func()) {
	held, release, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		v.hold(true)
		close(held)
		<-release
		v.hold(false)
		close(released)
	}()
	<-held
	return func() { close(release); <-released }
}

func TestFrameSizeDoesNotWaitForPresent(t *testing.T) {
	v := newTestView(t)
	resize(t, v.Handle(), 320, 180)
	tex := solid(t, 640, 360, 128)
	if _, err := v.Enqueue(tex, time.Now()); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	done := busy(v)
	enq := make(chan struct{})
	go func() { v.Enqueue(tex, time.Now()); close(enq) }() // blocks inside the present
	time.Sleep(50 * time.Millisecond)
	got := make(chan struct{})
	go func() { v.FrameSize(); v.Handle(); close(got) }()
	select {
	case <-got:
	case <-time.After(500 * time.Millisecond):
		done()
		<-enq
		t.Fatal("FrameSize/Handle waited for a frame being presented")
	}
	done()
	<-enq
}

func TestResizeDoesNotWaitForPresent(t *testing.T) {
	v := newTestView(t)
	resize(t, v.Handle(), 320, 180)
	if _, err := v.Enqueue(solid(t, 640, 360, 128), time.Now()); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	n := v.presents()
	done := busy(v)
	start := time.Now()
	resize(t, v.Handle(), 400, 300) // WM_SIZE on this (the window's) thread
	blocked := time.Since(start)
	if blocked > 200*time.Millisecond {
		done()
		t.Fatalf("WM_SIZE blocked %v while a frame was being presented", blocked)
	}
	go func() { time.Sleep(50 * time.Millisecond); done() }()
	pump(300 * time.Millisecond)
	if got := v.presents(); got <= n {
		t.Fatalf("presents = %d, want > %d: the skipped repaint was not retried", got, n)
	}
}

func TestNoSwapChainBeforePlacement(t *testing.T) {
	v := newTestView(t) // 1×1 until the UI toolkit places it
	if _, err := v.Enqueue(solid(t, 640, 360, 128), time.Now()); err != nil {
		t.Fatalf("Enqueue before placement: %v", err)
	}
	if n := v.presents(); n != 0 {
		t.Fatalf("presented %d times into an unplaced 1×1 window", n)
	}
	resize(t, v.Handle(), 320, 180)
	if n := v.presents(); n == 0 {
		t.Fatal("the frame received before placement was not shown once placed")
	}
}

func TestEnqueueWrongFormatKeepsFrameSize(t *testing.T) {
	v := newTestView(t)
	resize(t, v.Handle(), 320, 180)
	if _, err := v.Enqueue(solid(t, 640, 360, 128), time.Now()); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	bgra, err := newBGRA(100, 50)
	if err != nil {
		t.Fatalf("newBGRA: %v", err)
	}
	defer winmedia.Release(bgra)
	if _, err := v.Enqueue(bgra, time.Now()); err == nil {
		t.Fatal("Enqueue of a BGRA texture returned nil")
	}
	if w, h := v.FrameSize(); w != 640 || h != 360 {
		t.Fatalf("FrameSize = %dx%d after a rejected frame, want 640x360", w, h)
	}
}

func TestResizeReusesVideoProcessor(t *testing.T) {
	v := newTestView(t)
	resize(t, v.Handle(), 320, 180)
	tex := solid(t, 640, 360, 128)
	for i := 0; i < 5; i++ {
		if _, err := v.Enqueue(tex, time.Now()); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}
	if _, views := v.counts(); views > 2 {
		t.Errorf("%d output views for 5 frames into a 2-buffer swap chain, want ≤ 2", views)
	}
	for _, s := range [][2]int{{400, 300}, {500, 280}, {640, 480}} {
		resize(t, v.Handle(), s[0], s[1])
	}
	if procs, _ := v.counts(); procs != 1 {
		t.Errorf("%d video processors after 3 resizes with the same frame size, want 1", procs)
	}
}

func TestEnqueueAcrossDevices(t *testing.T) {
	v := newTestView(t)
	resize(t, v.Handle(), 320, 180)
	a := solid(t, 640, 360, 100)
	b, err := newOtherDeviceNV12(640, 360, 200)
	if err != nil {
		t.Skipf("second device: %v", err)
	}
	defer winmedia.Release(b)
	for i, tex := range []uintptr{a, b, a, b} {
		if _, err := v.Enqueue(tex, time.Now()); err != nil {
			t.Fatalf("Enqueue %d (device switch): %v", i, err)
		}
	}
	if n := v.presents(); n != 4 {
		t.Fatalf("presents = %d after 4 frames alternating devices, want 4", n)
	}
}

func TestEnqueueFromAnotherThreadWhileResizing(t *testing.T) {
	v := newTestView(t)
	resize(t, v.Handle(), 320, 180)
	tex := solid(t, 640, 360, 128)
	stop := make(chan struct{})
	errs := make(chan error, 1)
	go func() { // the decoder thread
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		for {
			select {
			case <-stop:
				errs <- nil
				return
			default:
			}
			if _, err := v.Enqueue(tex, time.Now()); err != nil {
				errs <- err
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	for i := 0; i < 40; i++ { // the window thread
		resize(t, v.Handle(), 300+i*10, 200+i*5)
		pump(5 * time.Millisecond)
	}
	close(stop)
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("Enqueue on the decoder thread: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("decoder thread did not stop: deadlock between Enqueue and window messages?")
	}
	if n := v.presents(); n == 0 {
		t.Error("nothing was presented")
	}
}
