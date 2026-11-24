//go:build darwin && cgo

// VideoToolbox H.264 encode/decode for package codec. Output callbacks run on
// VideoToolbox threads and hand data to Go through the exported mmEncOutput /
// mmDecOutput functions, identified by a cgo.Handle. No Go memory is kept by C.

#include "vt_darwin.h"
#include "_cgo_export.h"

#include <stdlib.h>
#include <string.h>
#include <CoreFoundation/CoreFoundation.h>
#include <CoreMedia/CoreMedia.h>
#include <CoreVideo/CoreVideo.h>
#include <VideoToolbox/VideoToolbox.h>

static void setInt(CFMutableDictionaryRef d, CFStringRef k, int32_t v) {
	CFNumberRef n = CFNumberCreate(NULL, kCFNumberSInt32Type, &v);
	CFDictionarySetValue(d, k, n);
	CFRelease(n);
}

static OSStatus setPropInt(VTSessionRef s, CFStringRef k, int32_t v) {
	CFNumberRef n = CFNumberCreate(NULL, kCFNumberSInt32Type, &v);
	OSStatus st = VTSessionSetProperty(s, k, n);
	CFRelease(n);
	return st;
}

// ---------------------------------------------------------------- encoder

struct mmEnc {
	VTCompressionSessionRef session;
	uintptr_t handle;
	int32_t lowLatency;
	int32_t hardware;
};

static void put4(uint8_t *p, const uint8_t *nal, size_t n) {
	p[0] = 0; p[1] = 0; p[2] = 0; p[3] = 1;
	memcpy(p + 4, nal, n);
}

static void encCallback(void *refcon, void *frameRefcon, OSStatus status,
                        VTEncodeInfoFlags flags, CMSampleBufferRef sb) {
	mmEnc *e = (mmEnc *)refcon;
	int64_t cap = (int64_t)(intptr_t)frameRefcon;
	if (status != noErr || sb == NULL || (flags & kVTEncodeInfo_FrameDropped)) {
		mmEncOutput(e->handle, NULL, 0, cap, 0, status != noErr ? status : -1);
		return;
	}
	if (!CMSampleBufferDataIsReady(sb)) {
		mmEncOutput(e->handle, NULL, 0, cap, 0, -1);
		return;
	}

	int key = 1;
	CFArrayRef atts = CMSampleBufferGetSampleAttachmentsArray(sb, false);
	if (atts != NULL && CFArrayGetCount(atts) > 0) {
		CFDictionaryRef a = CFArrayGetValueAtIndex(atts, 0);
		CFBooleanRef notSync = CFDictionaryGetValue(a, kCMSampleAttachmentKey_NotSync);
		if (notSync != NULL && CFBooleanGetValue(notSync)) key = 0;
	}

	CMFormatDescriptionRef fmt = CMSampleBufferGetFormatDescription(sb);
	int hdrLen = 4;
	size_t psCount = 0;
	const uint8_t *ps[2] = {NULL, NULL};
	size_t psLen[2] = {0, 0};
	if (fmt != NULL) {
		if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fmt, 0, NULL, NULL, &psCount, &hdrLen) != noErr) {
			hdrLen = 4;
		}
		if (key) {
			for (size_t i = 0; i < 2; i++) {
				CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fmt, i, &ps[i], &psLen[i], NULL, NULL);
			}
		}
	}

	CMBlockBufferRef bb = CMSampleBufferGetDataBuffer(sb);
	size_t total = CMBlockBufferGetDataLength(bb);
	uint8_t *src = malloc(total ? total : 1);
	if (CMBlockBufferCopyDataBytes(bb, 0, total, src) != kCMBlockBufferNoErr) {
		free(src);
		mmEncOutput(e->handle, NULL, 0, cap, 0, -1);
		return;
	}

	// Worst case growth: every NAL header becomes a 4-byte start code.
	size_t cap2 = total + (total / (size_t)(hdrLen > 0 ? hdrLen : 1) + 1) * 4 + psLen[0] + psLen[1] + 8;
	if (hdrLen >= 4) cap2 = total + psLen[0] + psLen[1] + 8;
	uint8_t *dst = malloc(cap2);
	size_t o = 0;
	if (key) {
		for (int i = 0; i < 2; i++) {
			if (ps[i] != NULL) { put4(dst + o, ps[i], psLen[i]); o += 4 + psLen[i]; }
		}
	}
	size_t off = 0;
	while (off + (size_t)hdrLen <= total) {
		size_t n = 0;
		for (int i = 0; i < hdrLen; i++) n = (n << 8) | src[off + i];
		off += hdrLen;
		if (n > total - off) break;
		put4(dst + o, src + off, n);
		o += 4 + n;
		off += n;
	}
	free(src);
	mmEncOutput(e->handle, dst, (int)o, cap, key, 0);
	free(dst);
}

static void encSetRate(mmEnc *e, int32_t bps) {
	setPropInt(e->session, kVTCompressionPropertyKey_AverageBitRate, bps);
	// DataRateLimits: [bytes, seconds] — allow 1.5x the average per second.
	int64_t bytes = (int64_t)bps * 3 / 2 / 8;
	double secs = 1.0;
	CFNumberRef nb = CFNumberCreate(NULL, kCFNumberSInt64Type, &bytes);
	CFNumberRef ns = CFNumberCreate(NULL, kCFNumberDoubleType, &secs);
	const void *vals[2] = {nb, ns};
	CFArrayRef arr = CFArrayCreate(NULL, vals, 2, &kCFTypeArrayCallBacks);
	VTSessionSetProperty(e->session, kVTCompressionPropertyKey_DataRateLimits, arr);
	CFRelease(arr);
	CFRelease(nb);
	CFRelease(ns);
}

static void encSetFPS(mmEnc *e, int32_t fps) {
	setPropInt(e->session, kVTCompressionPropertyKey_ExpectedFrameRate, fps);
	setPropInt(e->session, kVTCompressionPropertyKey_MaxKeyFrameInterval, fps * 2);
	setPropInt(e->session, kVTCompressionPropertyKey_MaxKeyFrameIntervalDuration, 2);
}

static OSStatus encTryCreate(mmEnc *e, int32_t w, int32_t h, int lowLatency) {
	CFMutableDictionaryRef spec = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(spec, kVTVideoEncoderSpecification_EnableHardwareAcceleratedVideoEncoder, kCFBooleanTrue);
	if (lowLatency) {
		if (__builtin_available(macOS 11.3, *)) {
			CFDictionarySetValue(spec, kVTVideoEncoderSpecification_EnableLowLatencyRateControl, kCFBooleanTrue);
		} else {
			CFRelease(spec);
			return -1;
		}
	}
	CFMutableDictionaryRef src = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	setInt(src, kCVPixelBufferPixelFormatTypeKey, kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange);
	setInt(src, kCVPixelBufferWidthKey, w);
	setInt(src, kCVPixelBufferHeightKey, h);

	OSStatus st = VTCompressionSessionCreate(NULL, w, h, kCMVideoCodecType_H264, spec, src, NULL,
		encCallback, e, &e->session);
	CFRelease(spec);
	CFRelease(src);
	return st;
}

int32_t mmEncCreate(int32_t w, int32_t h, int32_t fps, int32_t bps, uintptr_t handle, mmEnc **out) {
	mmEnc *e = calloc(1, sizeof(mmEnc));
	e->handle = handle;
	OSStatus st = encTryCreate(e, w, h, 1);
	e->lowLatency = (st == noErr);
	if (st != noErr) {
		e->session = NULL;
		st = encTryCreate(e, w, h, 0);
	}
	if (st != noErr) {
		free(e);
		return st;
	}
	VTSessionRef s = e->session;
	VTSessionSetProperty(s, kVTCompressionPropertyKey_RealTime, kCFBooleanTrue);
	VTSessionSetProperty(s, kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse);
	OSStatus pst = -1;
	if (__builtin_available(macOS 12.0, *)) {
		pst = VTSessionSetProperty(s, kVTCompressionPropertyKey_ProfileLevel,
			kVTProfileLevel_H264_ConstrainedBaseline_AutoLevel);
	}
	if (pst != noErr) {
		VTSessionSetProperty(s, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_H264_Baseline_AutoLevel);
	}
	encSetFPS(e, fps);
	encSetRate(e, bps);

	VTCompressionSessionPrepareToEncodeFrames(e->session);
	// -1: not reported (low-latency sessions return kVTPropertyNotSupportedErr).
	e->hardware = -1;
	CFBooleanRef hw = NULL;
	if (VTSessionCopyProperty(s, kVTCompressionPropertyKey_UsingHardwareAcceleratedVideoEncoder, NULL, &hw) == noErr && hw != NULL) {
		e->hardware = CFBooleanGetValue(hw);
		CFRelease(hw);
	}
	*out = e;
	return 0;
}

int32_t mmEncEncode(mmEnc *e, void *pixelBuffer, int64_t captureNanos, int32_t forceKey) {
	CMTime pts = CMTimeMake(captureNanos, 1000000000);
	CFDictionaryRef opts = NULL;
	if (forceKey) {
		const void *k[1] = {kVTEncodeFrameOptionKey_ForceKeyFrame};
		const void *v[1] = {kCFBooleanTrue};
		opts = CFDictionaryCreate(NULL, k, v, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	}
	OSStatus st = VTCompressionSessionEncodeFrame(e->session, (CVImageBufferRef)pixelBuffer, pts,
		kCMTimeInvalid, opts, (void *)(intptr_t)captureNanos, NULL);
	if (opts) CFRelease(opts);
	return st;
}

void mmEncSetBitrate(mmEnc *e, int32_t bps) { encSetRate(e, bps); }
void mmEncSetFPS(mmEnc *e, int32_t fps) { encSetFPS(e, fps); }
int32_t mmEncLowLatency(mmEnc *e) { return e->lowLatency; }
int32_t mmEncHardware(mmEnc *e) { return e->hardware; }

void mmEncDestroy(mmEnc *e) {
	if (e == NULL) return;
	if (e->session) {
		VTCompressionSessionCompleteFrames(e->session, kCMTimeInvalid);
		VTCompressionSessionInvalidate(e->session);
		CFRelease(e->session);
	}
	free(e);
}

// ---------------------------------------------------------------- decoder

struct mmDec {
	VTDecompressionSessionRef session;
	CMVideoFormatDescriptionRef fmt;
	uintptr_t handle;
};

static void decCallback(void *refcon, void *frameRefcon, OSStatus status, VTDecodeInfoFlags flags,
                        CVImageBufferRef img, CMTime pts, CMTime dur) {
	mmDec *d = (mmDec *)refcon;
	int64_t cap = (int64_t)(intptr_t)frameRefcon;
	if (status != noErr || img == NULL) {
		mmDecOutput(d->handle, 0, cap, status != noErr ? status : -1);
		return;
	}
	mmDecOutput(d->handle, (uintptr_t)img, cap, 0);
}

mmDec *mmDecNew(uintptr_t handle) {
	mmDec *d = calloc(1, sizeof(mmDec));
	d->handle = handle;
	return d;
}

static void decTeardown(mmDec *d) {
	if (d->session) {
		VTDecompressionSessionWaitForAsynchronousFrames(d->session);
		VTDecompressionSessionInvalidate(d->session);
		CFRelease(d->session);
		d->session = NULL;
	}
	if (d->fmt) {
		CFRelease(d->fmt);
		d->fmt = NULL;
	}
}

int32_t mmDecSetParams(mmDec *d, const uint8_t *sps, size_t spsLen, const uint8_t *pps, size_t ppsLen) {
	decTeardown(d);
	const uint8_t *ps[2] = {sps, pps};
	size_t sz[2] = {spsLen, ppsLen};
	OSStatus st = CMVideoFormatDescriptionCreateFromH264ParameterSets(NULL, 2, ps, sz, 4, &d->fmt);
	if (st != noErr) {
		d->fmt = NULL;
		return st;
	}

	CFMutableDictionaryRef spec = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(spec, kVTVideoDecoderSpecification_EnableHardwareAcceleratedVideoDecoder, kCFBooleanTrue);

	CFMutableDictionaryRef dst = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	setInt(dst, kCVPixelBufferPixelFormatTypeKey, kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange);
	CFDictionaryRef empty = CFDictionaryCreate(NULL, NULL, NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(dst, kCVPixelBufferIOSurfacePropertiesKey, empty);
	CFDictionarySetValue(dst, kCVPixelBufferMetalCompatibilityKey, kCFBooleanTrue);
	CFRelease(empty);

	VTDecompressionOutputCallbackRecord cb = {decCallback, d};
	st = VTDecompressionSessionCreate(NULL, d->fmt, spec, dst, &cb, &d->session);
	CFRelease(spec);
	CFRelease(dst);
	if (st != noErr) {
		d->session = NULL;
		CFRelease(d->fmt);
		d->fmt = NULL;
		return st;
	}
	VTSessionSetProperty(d->session, kVTDecompressionPropertyKey_RealTime, kCFBooleanTrue);
	return 0;
}

int32_t mmDecHasSession(mmDec *d) { return d->session != NULL; }

int32_t mmDecDecode(mmDec *d, const uint8_t *avcc, size_t len, int64_t captureNanos) {
	if (d->session == NULL) return -1;
	CMBlockBufferRef bb = NULL;
	OSStatus st = CMBlockBufferCreateWithMemoryBlock(NULL, NULL, len, kCFAllocatorDefault, NULL, 0, len,
		kCMBlockBufferAssureMemoryNowFlag, &bb);
	if (st != kCMBlockBufferNoErr) return st;
	st = CMBlockBufferReplaceDataBytes(avcc, bb, 0, len);
	if (st != kCMBlockBufferNoErr) {
		CFRelease(bb);
		return st;
	}
	CMSampleBufferRef sb = NULL;
	CMSampleTimingInfo timing = {kCMTimeInvalid, CMTimeMake(captureNanos, 1000000000), kCMTimeInvalid};
	st = CMSampleBufferCreateReady(NULL, bb, d->fmt, 1, 1, &timing, 1, &len, &sb);
	CFRelease(bb);
	if (st != noErr) return st;
	VTDecodeInfoFlags info = 0;
	// Synchronous decode: the output callback runs before this returns.
	st = VTDecompressionSessionDecodeFrame(d->session, sb, 0, (void *)(intptr_t)captureNanos, &info);
	CFRelease(sb);
	if (st == noErr) VTDecompressionSessionWaitForAsynchronousFrames(d->session);
	return st;
}

void mmDecDestroy(mmDec *d) {
	if (d == NULL) return;
	decTeardown(d);
	free(d);
}

// ---------------------------------------------------------------- test helpers
// Used by codec_darwin_test.go (cgo is not allowed in _test.go files).

void *mmTestNewNV12(int32_t w, int32_t h, int32_t *status) {
	CFDictionaryRef empty = CFDictionaryCreate(NULL, NULL, NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	const void *k[1] = {kCVPixelBufferIOSurfacePropertiesKey};
	const void *v[1] = {empty};
	CFDictionaryRef attrs = CFDictionaryCreate(NULL, k, v, 1,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CVPixelBufferRef pb = NULL;
	*status = CVPixelBufferCreate(NULL, w, h, kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange, attrs, &pb);
	CFRelease(attrs);
	CFRelease(empty);
	return pb;
}

void mmTestFill(void *p, int32_t frame) {
	CVPixelBufferRef pb = (CVPixelBufferRef)p;
	CVPixelBufferLockBaseAddress(pb, 0);
	size_t w = CVPixelBufferGetWidthOfPlane(pb, 0), h = CVPixelBufferGetHeightOfPlane(pb, 0);
	size_t stride = CVPixelBufferGetBytesPerRowOfPlane(pb, 0);
	uint8_t *y = CVPixelBufferGetBaseAddressOfPlane(pb, 0);
	for (size_t r = 0; r < h; r++) {
		uint8_t *row = y + r * stride;
		for (size_t c = 0; c < w; c++) row[c] = (uint8_t)(16 + ((c + r + (size_t)frame * 8) % 220));
	}
	size_t uh = CVPixelBufferGetHeightOfPlane(pb, 1);
	size_t ustride = CVPixelBufferGetBytesPerRowOfPlane(pb, 1);
	uint8_t *uv = CVPixelBufferGetBaseAddressOfPlane(pb, 1);
	for (size_t r = 0; r < uh; r++) memset(uv + r * ustride, 128, ustride);
	CVPixelBufferUnlockBaseAddress(pb, 0);
}

void mmTestRelease(void *p) { if (p) CVPixelBufferRelease((CVPixelBufferRef)p); }

void mmTestSize(void *p, int32_t *w, int32_t *h) {
	*w = (int32_t)CVPixelBufferGetWidth((CVPixelBufferRef)p);
	*h = (int32_t)CVPixelBufferGetHeight((CVPixelBufferRef)p);
}
