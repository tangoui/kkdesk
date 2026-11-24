// Media Foundation H.264 encoder (hardware MFT preferred, software
// CMSH264EncoderMFT fallback). Plain C declarations shared by
// encode_windows.go (cgo) and encode_windows.cpp.
#ifndef MMDESK_ENCODE_WINDOWS_H
#define MMDESK_ENCODE_WINDOWS_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// mmencLog is implemented in Go (encode_windows.go).
void mmencLog(char *msg);

typedef struct mmEncW mmEncW;

typedef struct mmencConfig {
    int32_t width, height, fps, bps;
} mmencConfig;

typedef struct mmencOut {
    const uint8_t *data; // valid until the next mmenc_output call
    int32_t len;
    int64_t sampleTime;  // 100 ns units, as passed to mmenc_input
    int32_t hasTime;
    int32_t cleanPoint;
} mmencOut;

// Extra results besides HRESULTs.
#define MMENC_S_RETRY 2                         // stream changed; call mmenc_output again
#define MMENC_E_DEVICE ((int32_t)0xA0DE0001)    // texture is from another D3D11 device
#define MMENC_E_SIZE ((int32_t)0xA0DE0002)      // texture size differs from the encoder's

// METransform* event types delivered by mmenc_get_event.
#define MMENC_EV_NEED_INPUT 601
#define MMENC_EV_HAVE_OUTPUT 602
#define MMENC_EV_DRAIN_COMPLETE 603

void mmenc_thread_init(void);
// Tries every hardware H.264 encoder MFT on the adapter (logging each);
// name receives the friendly name of the one in use.
int32_t mmenc_create_hw(void *device, void *manager, uint64_t luid, const mmencConfig *cfg, mmEncW **out,
                        char *name, int32_t nameLen);
// Creates the software CMSH264EncoderMFT (CPU NV12 input via a staging copy).
int32_t mmenc_create_sw(void *device, const mmencConfig *cfg, mmEncW **out);
int32_t mmenc_is_async(mmEncW *e);
// Gets the next MFT event (async MFTs). wait=0 returns MF_E_NO_EVENTS_AVAILABLE
// instead of blocking. status receives the event's HRESULT.
int32_t mmenc_get_event(mmEncW *e, int32_t wait, uint32_t *type, int32_t *status);
// Whether mmenc_get_event may block (the MFT supports IMFShutdown, which
// unblocks it on close).
int32_t mmenc_can_block(mmEncW *e);
int32_t mmenc_input(mmEncW *e, void *texture, int64_t sampleTime, int64_t duration, int32_t forceKey);
int32_t mmenc_output(mmEncW *e, mmencOut *o);
int32_t mmenc_seq_header(mmEncW *e, const uint8_t **data, int32_t *len);
int32_t mmenc_set_bitrate(mmEncW *e, uint32_t bps);
int32_t mmenc_set_gop(mmEncW *e, uint32_t frames);
int32_t mmenc_drain(mmEncW *e);
void mmenc_shutdown(mmEncW *e);
void mmenc_destroy(mmEncW *e);

#ifdef __cplusplus
}
#endif

#endif
