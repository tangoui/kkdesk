#ifndef KKDESK_CAPTURE_DARWIN_H
#define KKDESK_CAPTURE_DARWIN_H

#include <stdint.h>
#include <stddef.h>

// mmcap_start starts a ScreenCaptureKit stream of the main display with
// minimumFrameInterval = intervalNs. On
// success it returns an opaque session and stores the output size; on
// failure it returns NULL and stores a malloc'ed message in *err.
void *mmcap_start(int64_t intervalNs, int maxWidth, const char *excludeApp,
                  const uint32_t *ids, size_t count, uintptr_t handle, int *outW, int *outH, char **err);
char *mmcap_exclude(void *s, const char *excludeApp, const uint32_t *ids, size_t count);
void mmcap_set_fps(void *s, int64_t intervalNs);
// mmcap_stop stops the stream, waits until no frame callback is running
// and releases the session.
void mmcap_stop(void *s);

int mmcap_has_permission(void);
void mmcap_request_permission(void);
uint32_t mmcap_pixel_format(uintptr_t pixelBuffer);
uint64_t mmcap_now_ns(void);

#endif
