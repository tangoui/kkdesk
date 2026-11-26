// Media Foundation H.264 decoder (CLSID_MSH264DecoderMFT, D3D11 hardware
// decoding when available, software otherwise). Plain C declarations shared
// by decode_windows.go (cgo) and decode_windows.cpp.
#ifndef KKDESK_DECODE_WINDOWS_H
#define KKDESK_DECODE_WINDOWS_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// mmdecLog is implemented in Go (decode_windows.go).
void mmdecLog(char *msg);

typedef struct mmDecW mmDecW;

typedef struct mmdecOut {
    void *texture;      // decoder-owned width×height NV12 ID3D11Texture2D*, valid until the next mmdec_output
    int32_t width, height;
    int64_t sampleTime; // 100 ns units, as passed to mmdec_input
} mmdecOut;

// Extra results besides HRESULTs.
#define MMDEC_S_RETRY 2 // output type renegotiated after a stream change; call mmdec_output again

// Creates the decoder on device (the IMFDXGIDeviceManager wraps the same
// device). software=1 skips D3D11 decoding. info receives a description
// of the decoder in use ("... (D3D11)" or "... (software: <reason>)").
int32_t mmdec_create(void *device, void *manager, int32_t software, mmDecW **out, char *info, int32_t infoLen);
// Submits one Annex-B access unit. Returns MF_E_NOTACCEPTING when output
// must be drained first.
int32_t mmdec_input(mmDecW *d, const uint8_t *annexB, int32_t len, int64_t sampleTime);
// Gets one decoded picture: S_OK, MF_E_TRANSFORM_NEED_MORE_INPUT when none
// is pending, MMDEC_S_RETRY after a stream change, or an error.
int32_t mmdec_output(mmDecW *d, mmdecOut *o);
void mmdec_destroy(mmDecW *d);

#ifdef __cplusplus
}
#endif

#endif
