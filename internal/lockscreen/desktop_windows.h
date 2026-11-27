#pragma once
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif
int kklock_state(uint32_t session);
uintptr_t kklock_open_desktop(void);
int kklock_enter_desktop(uintptr_t desktop);
void kklock_close_desktop(uintptr_t desktop);
int kklock_is_winlogon(uintptr_t desktop);
int kklock_tap(uint16_t key);
int kklock_password(const uint16_t *password, uint32_t count);

typedef struct kklock_capture kklock_capture;
int32_t kklock_open_capture(kklock_capture **capture);
void kklock_close_capture(kklock_capture *capture);
int32_t kklock_grab(kklock_capture *capture, void *pixels, uint32_t limit,
    uint32_t *width, uint32_t *height, uint32_t *rotation);
int32_t kklock_upload(void *device, void **texture, const void *pixels,
    uint32_t width, uint32_t height);
#ifdef __cplusplus
}
#endif
