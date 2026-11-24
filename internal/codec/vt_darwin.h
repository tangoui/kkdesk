// VideoToolbox helpers for package codec (darwin, cgo).
#ifndef MMDESK_CODEC_VT_H
#define MMDESK_CODEC_VT_H

#include <stdint.h>
#include <stddef.h>

typedef struct mmEnc mmEnc;
typedef struct mmDec mmDec;

// Encoder. handle is a cgo.Handle passed back to mmEncOutput.
// On success *out is set and 0 returned; otherwise an OSStatus.
int32_t mmEncCreate(int32_t w, int32_t h, int32_t fps, int32_t bps, uintptr_t handle, mmEnc **out);
int32_t mmEncEncode(mmEnc *e, void *pixelBuffer, int64_t captureNanos, int32_t forceKey);
void mmEncSetBitrate(mmEnc *e, int32_t bps);
void mmEncSetFPS(mmEnc *e, int32_t fps);
int32_t mmEncLowLatency(mmEnc *e);
// mmEncHardware: 1 hardware, 0 software, -1 not reported by the session.
int32_t mmEncHardware(mmEnc *e);
void mmEncDestroy(mmEnc *e);

// Decoder. handle is a cgo.Handle passed back to mmDecOutput.
mmDec *mmDecNew(uintptr_t handle);
// mmDecSetParams (re)creates the session from SPS/PPS.
int32_t mmDecSetParams(mmDec *d, const uint8_t *sps, size_t spsLen, const uint8_t *pps, size_t ppsLen);
int32_t mmDecHasSession(mmDec *d);
// mmDecDecode decodes one AVCC (4-byte length-prefixed) access unit and waits
// for its output.
int32_t mmDecDecode(mmDec *d, const uint8_t *avcc, size_t len, int64_t captureNanos);
void mmDecDestroy(mmDec *d);

// Test helpers.
void *mmTestNewNV12(int32_t w, int32_t h, int32_t *status);
void mmTestFill(void *pixelBuffer, int32_t frame);
void mmTestRelease(void *pixelBuffer);
void mmTestSize(void *pixelBuffer, int32_t *w, int32_t *h);

#endif
