#ifndef KKDESK_PRIVACY_WINDOWS_H
#define KKDESK_PRIVACY_WINDOWS_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

void *mmprivacyw_open(uintptr_t *window, unsigned long *error);
unsigned long mmprivacyw_run(void *screen);
void mmprivacyw_stop(uintptr_t window);
unsigned long mmprivacyw_lock(void);
unsigned long mmprivacyw_locked(int *locked);

#ifdef __cplusplus
}
#endif

#endif
