// Windows video view: an HWND with a flip-model DXGI swap chain; NV12
// frames are scaled (aspect fit, black bars) and converted to BGRA by a
// D3D11 video processor. Plain C declarations shared by display_windows.go
// (cgo) and display_windows.cpp.
#ifndef KKDESK_DISPLAY_WINDOWS_H
#define KKDESK_DISPLAY_WINDOWS_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// mmdispwLog is implemented in Go (display_windows.go).
void mmdispwLog(char *msg);

typedef struct mmDispW mmDispW;

// Creates the (hidden, top-level) window on the calling thread, which must
// run its message loop. hwnd receives the HWND.
int32_t mmdispw_new(mmDispW **out, void **hwnd);
// Copies the NV12 texture tex (any D3D11 device; the swap chain follows it)
// as the current frame and presents it. w, h receive its size.
int32_t mmdispw_enqueue(mmDispW *d, void *tex, int32_t *w, int32_t *h);
// Draws the current frame into the BGRA texture target (sized like the
// window would be) without presenting. For tests.
int32_t mmdispw_render_to(mmDispW *d, void *target);
// Enters (on=1) or leaves (on=0) the view's critical section, as a frame
// being presented on another thread would. For tests; the same thread must
// enter and leave.
void mmdispw_hold(mmDispW *d, int32_t on);
// Counters for tests: video processors and output views created so far.
void mmdispw_counts(mmDispW *d, int32_t *processors, int32_t *outputViews);
// Number of successful Present calls.
int32_t mmdispw_presents(mmDispW *d);
// DWM composition timing: the QPC value of the last vblank, the refresh
// period in QPC ticks, the current QPC value and the QPC frequency.
int32_t mmdispw_vsync(int64_t *vblank, int64_t *period, int64_t *now, int64_t *freq);
// Handles messages sent to the calling thread's windows from other threads
// (only those: nothing posted is dispatched). For a thread waiting on a lock
// held by a thread that may SendMessage to it.
void mmdispw_pump_sent(void);
// Destroys the window (calling thread must own it) and releases everything.
void mmdispw_destroy(mmDispW *d);

#ifdef __cplusplus
}
#endif

#endif
