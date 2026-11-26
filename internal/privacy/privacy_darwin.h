#ifndef KKDESK_PRIVACY_DARWIN_H
#define KKDESK_PRIVACY_DARWIN_H

#include <stddef.h>
#include <stdint.h>

void *mmprivacy_new(uintptr_t handle, int *status);
uint32_t *mmprivacy_prepare(void *screen, size_t *count);
void mmprivacy_show(void *screen);
void mmprivacy_countdown(void *screen, int seconds);
void mmprivacy_close(void *screen);

const char *mmprivacy_lock(void);
int mmprivacy_locked(void);

#endif
