//go:build darwin && cgo

// AVSampleBufferDisplayLayer view for package display. Enqueue may run on
// any thread; the Go side serializes all calls on one display object.

#include "display_darwin.h"

#import <AppKit/AppKit.h>
#import <AVFoundation/AVFoundation.h>
#include <CoreMedia/CoreMedia.h>
#include <CoreVideo/CoreVideo.h>
#include <mach/mach_time.h>
#include <stdatomic.h>
#include <stdlib.h>

// vsyncState is written by the display link thread on every refresh and read
// by mmdisp_vsync. It is malloc'd so it outlives no Objective-C object.
typedef struct {
	_Atomic uint64_t host;   // host time of the latest vsync (mach ticks)
	_Atomic int64_t period;  // refresh period (ns)
} vsyncState;

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"

@interface MMDisplay : NSObject
@property(nonatomic, strong) NSView *view;
@property(nonatomic, strong) AVSampleBufferDisplayLayer *layer;
@property(nonatomic) CMVideoFormatDescriptionRef format;
@property(nonatomic) CVDisplayLinkRef link;
@property(nonatomic) BOOL linkStarted;
@property(nonatomic) vsyncState *vs;
@end

static CVReturn linkCallback(CVDisplayLinkRef l, const CVTimeStamp *inNow, const CVTimeStamp *inOutTime,
	CVOptionFlags in, CVOptionFlags *flags, void *ctx);

@implementation MMDisplay
- (void)dealloc {
	if (_format) CFRelease(_format);
	if (_link) {
		if (_linkStarted) CVDisplayLinkStop(_link);
		CVDisplayLinkRelease(_link);
	}
	free(_vs); // the link is stopped (CVDisplayLinkStop waits for the callback)
}
@end

// The renderer the layer feeds: sampleBufferRenderer on macOS 14+, the layer
// itself before (its enqueue/flush/status API is deprecated since 14).
static id renderer(MMDisplay *d) {
	if (@available(macOS 14.0, *)) return d.layer.sampleBufferRenderer;
	return d.layer;
}

static int32_t rstatus(MMDisplay *d) {
	if (@available(macOS 14.0, *)) return (int32_t)d.layer.sampleBufferRenderer.status;
	return (int32_t)d.layer.status;
}

static BOOL needsFlush(MMDisplay *d) {
	if (@available(macOS 14.0, *)) {
		AVSampleBufferVideoRenderer *r = d.layer.sampleBufferRenderer;
		return r.status == AVQueuedSampleBufferRenderingStatusFailed || r.requiresFlushToResumeDecoding;
	}
	return d.layer.status == AVQueuedSampleBufferRenderingStatusFailed;
}

void *mmdisp_new(int withView) {
	@autoreleasepool {
		MMDisplay *d = [MMDisplay new];
		AVSampleBufferDisplayLayer *layer = [AVSampleBufferDisplayLayer new];
		layer.videoGravity = AVLayerVideoGravityResizeAspect;
		layer.backgroundColor = CGColorGetConstantColor(kCGColorBlack);
		d.layer = layer;
		if (withView) {
			NSView *v = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, 1, 1)];
			// Layer-hosting view: set the layer first, then wantsLayer.
			v.layer = layer;
			v.wantsLayer = YES;
			d.view = v;
		}
		CVDisplayLinkRef link = NULL;
		if (CVDisplayLinkCreateWithCGDisplay(CGMainDisplayID(), &link) == kCVReturnSuccess) {
			d.link = link;
			d.vs = calloc(1, sizeof(vsyncState));
			// The link runs only to record vsync timestamps (no Go, no locks).
			CVDisplayLinkSetOutputCallback(link, linkCallback, d.vs);
			if (CVDisplayLinkStart(link) == kCVReturnSuccess) d.linkStarted = YES;
		}
		return (__bridge_retained void *)d;
	}
}

void *mmdisp_view(void *p) {
	MMDisplay *d = (__bridge MMDisplay *)p;
	return (__bridge void *)d.view;
}

static OSStatus enqueueOnce(MMDisplay *d, CMSampleBufferRef sb) {
	id r = renderer(d);
	if (needsFlush(d)) [r flush];
	[r enqueueSampleBuffer:sb];
	return rstatus(d) == AVQueuedSampleBufferRenderingStatusFailed ? -1 : 0;
}

int32_t mmdisp_enqueue(void *p, void *pixp, int32_t *w, int32_t *h) {
	@autoreleasepool {
		MMDisplay *d = (__bridge MMDisplay *)p;
		CVPixelBufferRef pix = (CVPixelBufferRef)pixp;
		*w = (int32_t)CVPixelBufferGetWidth(pix);
		*h = (int32_t)CVPixelBufferGetHeight(pix);
		if (d.format == NULL || !CMVideoFormatDescriptionMatchesImageBuffer(d.format, pix)) {
			CMVideoFormatDescriptionRef f = NULL;
			OSStatus st = CMVideoFormatDescriptionCreateForImageBuffer(NULL, pix, &f);
			if (st != noErr) return st;
			if (d.format) CFRelease(d.format);
			d.format = f;
		}
		CMSampleTimingInfo t = {
			.duration = kCMTimeInvalid,
			.presentationTimeStamp = CMClockGetTime(CMClockGetHostTimeClock()),
			.decodeTimeStamp = kCMTimeInvalid,
		};
		CMSampleBufferRef sb = NULL;
		OSStatus st = CMSampleBufferCreateReadyWithImageBuffer(NULL, pix, d.format, &t, &sb);
		if (st != noErr) return st;
		CFArrayRef atts = CMSampleBufferGetSampleAttachmentsArray(sb, true);
		if (atts && CFArrayGetCount(atts) > 0) {
			CFMutableDictionaryRef a = (CFMutableDictionaryRef)CFArrayGetValueAtIndex(atts, 0);
			CFDictionarySetValue(a, kCMSampleAttachmentKey_DisplayImmediately, kCFBooleanTrue);
		}
		st = enqueueOnce(d, sb);
		if (st != 0) {
			[renderer(d) flush];
			st = enqueueOnce(d, sb);
		}
		CFRelease(sb);
		return st;
	}
}

int32_t mmdisp_status(void *p) { return rstatus((__bridge MMDisplay *)p); }

static int64_t hostToNs(uint64_t t) {
	static mach_timebase_info_data_t tb;
	if (tb.denom == 0) mach_timebase_info(&tb);
	return (int64_t)((__uint128_t)t * tb.numer / tb.denom);
}

// linkCallback runs on the display link thread once per refresh. inOutTime is
// the predicted display time of the refresh being prepared; its host times lie
// on the refresh grid (inNow's host time is the callback time, with jitter).
static CVReturn linkCallback(CVDisplayLinkRef l, const CVTimeStamp *inNow, const CVTimeStamp *inOutTime,
	CVOptionFlags in, CVOptionFlags *flags, void *ctx) {
	vsyncState *vs = ctx;
	const CVTimeStamp *t = inOutTime;
	if ((t->flags & kCVTimeStampHostTimeValid) && t->hostTime != 0) {
		if ((t->flags & kCVTimeStampVideoRefreshPeriodValid) && t->videoTimeScale > 0 && t->videoRefreshPeriod > 0) {
			double rate = (t->flags & kCVTimeStampRateScalarValid) && t->rateScalar > 0 ? t->rateScalar : 1.0;
			atomic_store(&vs->period, (int64_t)((double)t->videoRefreshPeriod * 1e9 / (double)t->videoTimeScale / rate));
		}
		atomic_store(&vs->host, t->hostTime);
	}
	return kCVReturnSuccess;
}

int32_t mmdisp_vsync(void *p, int64_t *untilNs, int64_t *periodNs, int32_t *hasPhase) {
	MMDisplay *d = (__bridge MMDisplay *)p;
	*hasPhase = 0;
	if (d.link == NULL) return -1;
	uint64_t last = d.vs ? atomic_load(&d.vs->host) : 0;
	int64_t period = d.vs ? atomic_load(&d.vs->period) : 0;
	if (period <= 0) {
		CVTime nom = CVDisplayLinkGetNominalOutputVideoRefreshPeriod(d.link);
		if (nom.timeScale <= 0 || nom.timeValue <= 0) return -1;
		period = (int64_t)((double)nom.timeValue * 1e9 / (double)nom.timeScale);
	}
	*periodNs = period;
	if (last != 0) {
		*untilNs = hostToNs(last) - hostToNs(mach_absolute_time());
		*hasPhase = 1;
	}
	return 0;
}

void mmdisp_destroy(void *p) {
	MMDisplay *d = (__bridge_transfer MMDisplay *)p;
	if (@available(macOS 14.0, *)) {
		[d.layer.sampleBufferRenderer flushWithRemovalOfDisplayedImage:YES completionHandler:nil];
	} else {
		[d.layer flushAndRemoveImage];
	}
	if (d.link && d.linkStarted) {
		CVDisplayLinkStop(d.link);
		d.linkStarted = NO;
	}
	NSView *v = d.view;
	if (v) {
		if ([NSThread isMainThread]) {
			[v removeFromSuperview];
		} else {
			dispatch_async(dispatch_get_main_queue(), ^{ [v removeFromSuperview]; });
		}
	}
	d = nil; // ARC releases the view, layer, format and link.
}

void *mmdisp_test_nv12(int32_t w, int32_t h, int32_t *status) {
	NSDictionary *attrs = @{(__bridge NSString *)kCVPixelBufferIOSurfacePropertiesKey : @{}};
	CVPixelBufferRef pb = NULL;
	*status = CVPixelBufferCreate(NULL, w, h, kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange,
		(__bridge CFDictionaryRef)attrs, &pb);
	if (*status == kCVReturnSuccess) {
		CVPixelBufferLockBaseAddress(pb, 0);
		for (size_t i = 0; i < CVPixelBufferGetPlaneCount(pb); i++) {
			memset(CVPixelBufferGetBaseAddressOfPlane(pb, i), i == 0 ? 64 : 128,
				CVPixelBufferGetBytesPerRowOfPlane(pb, i) * CVPixelBufferGetHeightOfPlane(pb, i));
		}
		CVPixelBufferUnlockBaseAddress(pb, 0);
	}
	return pb;
}

void mmdisp_test_release(void *pix) { CVPixelBufferRelease((CVPixelBufferRef)pix); }

#pragma clang diagnostic pop
