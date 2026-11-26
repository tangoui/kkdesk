#pragma once
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

int spike_lock_state(uint32_t session, uint32_t *error);
intptr_t spike_enter_desktop(uint32_t session, uint32_t *error);
void spike_leave_desktop(intptr_t desk);
int spike_secure_desktop(uint32_t session, uint32_t *error);
int32_t spike_capture(uint8_t **rgba, uint32_t *width, uint32_t *height, const char **stage);
unsigned spike_post(uint32_t *error);

#ifdef __cplusplus
}
#endif
