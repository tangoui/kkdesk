#pragma once
#include <stddef.h>
#include <stdint.h>

typedef struct {
    char team[64];
    char identifier[256];
    uint32_t uid;
} kk_mac_identity;

int kk_mac_self(kk_mac_identity *out);
int kk_mac_peer(int fd, kk_mac_identity *out);
int kk_mac_console(uint32_t *uid);
int kk_mac_console_locked(uint32_t uid);
int kk_mac_job_status(int agent);
int kk_mac_job_change(int agent, int install);
int kk_mac_shm(const char *name, size_t size, int *writer, int *reader);
int kk_mac_inject_password(const uint16_t *password, size_t size);
int kk_mac_read_nv12(uintptr_t pixel, uint8_t *dst, size_t size, uint32_t w, uint32_t h);
uintptr_t kk_mac_make_nv12(const uint8_t *src, size_t size, uint32_t w, uint32_t h);
void kk_mac_release_nv12(uintptr_t pixel);
