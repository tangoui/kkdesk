// Desktop Duplication (DXGI 1.2) capture of the primary display. Plain C
// declarations shared by capture_windows.go (cgo) and capture_windows.cpp.
#ifndef MMDESK_CAPTURE_WINDOWS_H
#define MMDESK_CAPTURE_WINDOWS_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// mmcapLog is implemented in Go (capture_windows.go).
void mmcapLog(char *msg);

typedef struct mmcapDup mmcapDup;

// Result codes of mmcap_acquire (HRESULT failures are returned as is).
enum {
    MMCAP_FRAME = 0,   // a new desktop image was copied into the copy texture
    MMCAP_TIMEOUT = 1, // nothing changed within the timeout
    MMCAP_NOIMAGE = 2, // only the pointer changed (no new image)
};

typedef struct mmcapFrame {
    void *texture;       // copy texture (ID3D11Texture2D*), owned by the dup
    int32_t width, height;
    int32_t format;      // DXGI_FORMAT
    int64_t presentQpc;  // DXGI_OUTDUPL_FRAME_INFO.LastPresentTime
    int64_t arrivalQpc;  // QueryPerformanceCounter when AcquireNextFrame returned
    uint32_t accumulated;
    float acquireMs;     // time spent in AcquireNextFrame
} mmcapFrame;

typedef struct mmcapInfo {
    int32_t width, height;  // desktop image size (native panel orientation)
    int32_t rotation;       // DXGI_MODE_ROTATION
    int32_t format;         // DXGI_FORMAT of the duplicated image
    int32_t refreshNum, refreshDen;
    int32_t inSystemMemory;
    int32_t duplicateOutput1; // 1 when IDXGIOutput5::DuplicateOutput1 was used
} mmcapInfo;

// Creates the duplication of the primary output of device's adapter.
int32_t mmcap_open(void *device, mmcapDup **out, mmcapInfo *info);
// Waits up to timeoutMs for a new image.
int32_t mmcap_acquire(mmcapDup *d, uint32_t timeoutMs, mmcapFrame *f);
void mmcap_close(mmcapDup *d);
int64_t mmcap_qpc(void);
int64_t mmcap_qpf(void);
// Marks the thread as a capture thread (MMCSS "Capture", COM MTA).
void mmcap_thread_init(void);

#ifdef __cplusplus
}
#endif

#endif
