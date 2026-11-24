// Shared Direct3D 11 / Media Foundation helpers for the Windows capture and
// encoder backends. Plain C declarations: included from cgo preambles (C)
// and from winmedia_windows.cpp (C++).
#ifndef KKDESK_WINMEDIA_WINDOWS_H
#define KKDESK_WINMEDIA_WINDOWS_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// mmwmLog is implemented in Go (winmedia_windows.go).
void mmwmLog(char *msg);

// Initializes COM (multithreaded apartment) on the calling thread; repeated
// calls are harmless.
void mmwm_com_init(void);

// Creates the D3D11 device on the adapter that owns the primary display
// (VIDEO_SUPPORT|BGRA_SUPPORT, multithread protected), starts Media
// Foundation and wraps the device in an IMFDXGIDeviceManager. Every step is
// logged. Returns an HRESULT.
int32_t mmwm_create_device(void **device, void **manager, uint64_t *luid);

// Returns ID3D11Device::GetDeviceRemovedReason (S_OK while usable).
int32_t mmwm_device_removed_reason(void *device);

// IUnknown::Release (NULL is ignored).
void mmwm_release(void *unk);

// Video processor converting a BGRA (or other RGB) texture to an NV12
// texture of another size (scaling, optional rotation, BT.709 limited).
typedef struct mmwmConv mmwmConv;
int32_t mmwm_conv_create(void *device, int inW, int inH, int inFormat, int outW, int outH,
                         int rotation, mmwmConv **out);
int32_t mmwm_conv_blt(mmwmConv *c, void *srcTexture, void *dstTexture);
void mmwm_conv_destroy(mmwmConv *c);

// Creates a w×h NV12 texture (D3D11_USAGE_DEFAULT, render target) on device.
int32_t mmwm_new_nv12(void *device, int w, int h, void **texture);

#ifdef __cplusplus
}
#endif

#endif
