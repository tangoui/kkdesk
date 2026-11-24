#ifndef MMDESK_DISPLAY_DARWIN_H
#define MMDESK_DISPLAY_DARWIN_H

#include <stdint.h>

// mmdisp_new creates a display. withView=1 creates the layer-hosting NSView
// too (main thread only); withView=0 creates only the layer (tests).
// Returns a retained opaque object, or NULL.
void *mmdisp_new(int withView);
void *mmdisp_view(void *d);
// mmdisp_enqueue wraps pix in a CMSampleBuffer and enqueues it. Returns 0,
// an OSStatus, or -1 if the renderer stays Failed after a flush and retry.
int32_t mmdisp_enqueue(void *d, void *pix, int32_t *w, int32_t *h);
// mmdisp_status returns the renderer status (AVQueuedSampleBufferRenderingStatus).
int32_t mmdisp_status(void *d);
// mmdisp_vsync reports the refresh period of the main display (ns) and, when
// hasPhase is set, the time of a vsync on the refresh grid relative to the
// moment of the call (ns, may be in the past or future). Returns 0 on success.
int32_t mmdisp_vsync(void *d, int64_t *untilNs, int64_t *periodNs, int32_t *hasPhase);
// mmdisp_destroy removes the view from its superview, flushes the renderer,
// stops the display link and releases d.
void mmdisp_destroy(void *d);

void *mmdisp_test_nv12(int32_t w, int32_t h, int32_t *status);
void mmdisp_test_release(void *pix);

#endif
