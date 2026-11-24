#ifndef KKDESK_INJECT_DARWIN_H
#define KKDESK_INJECT_DARWIN_H

#include <stdint.h>

int mminj_has_permission(void);
void mminj_request_permission(void);
void mminj_main_bounds(double *x, double *y, double *w, double *h);
double mminj_double_click_interval(void);

// Each returns 0 on success, -1 if the event could not be created.
int mminj_mouse(int type, double x, double y, int button, int64_t clickState, uint64_t flags);
int mminj_key(uint16_t keycode, int down, int flagsChanged, uint64_t flags);
int mminj_scroll(int32_t dx, int32_t dy, uint64_t flags);

#endif
