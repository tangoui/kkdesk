#import <Foundation/Foundation.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <CoreGraphics/CoreGraphics.h>
#include <mach/mach_time.h>
#include <libproc.h>
#include <stdlib.h>
#include <string.h>

#include "capture_darwin.h"
#include "_cgo_export.h"

static const int64_t kWaitSeconds = 5;
static void *kQueueKey = &kQueueKey;

static uint64_t machToNs(uint64_t t) {
	static mach_timebase_info_data_t tb;
	static dispatch_once_t once;
	dispatch_once(&once, ^{ mach_timebase_info(&tb); });
	return (uint64_t)((__uint128_t)t * tb.numer / tb.denom);
}

uint64_t mmcap_now_ns(void) { return machToNs(mach_absolute_time()); }

static char *dupErr(NSString *msg) { return strdup(msg.UTF8String ?: "capture: unknown error"); }

static BOOL waitSem(dispatch_semaphore_t sem) {
	return dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, kWaitSeconds * NSEC_PER_SEC)) == 0;
}

API_AVAILABLE(macos(12.3))
@interface MMCapture : NSObject <SCStreamOutput, SCStreamDelegate>
@property(nonatomic, strong) SCStream *stream;
@property(nonatomic, strong) SCStreamConfiguration *config;
@property(nonatomic, strong) dispatch_queue_t queue;
@property(atomic) BOOL stopped;
@property(nonatomic) uintptr_t handle;
@end

@implementation MMCapture

- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)sb ofType:(SCStreamOutputType)type {
	if (type != SCStreamOutputTypeScreen || self.stopped || !CMSampleBufferIsValid(sb)) {
		return;
	}
	uint64_t entry = mach_absolute_time();
	CFArrayRef atts = CMSampleBufferGetSampleAttachmentsArray(sb, false);
	if (atts == NULL || CFArrayGetCount(atts) == 0) {
		mmcapGoStatus(self.handle, -1);
		return;
	}
	NSDictionary *info = (__bridge NSDictionary *)CFArrayGetValueAtIndex(atts, 0);
	NSNumber *status = info[SCStreamFrameInfoStatus];
	if (status == nil || status.integerValue != SCFrameStatusComplete) {
		// Idle / Blank / Suspended / Started / Stopped: no new image
		mmcapGoStatus(self.handle, status != nil ? (int)status.integerValue : -1);
		return;
	}
	CVPixelBufferRef pb = CMSampleBufferGetImageBuffer(sb);
	if (pb == NULL) {
		mmcapGoStatus(self.handle, -1);
		return;
	}
	// SCStreamFrameInfoDisplayTime is mach absolute time (ticks), usually a
	// few ms after entry (the vsync the frame is shown at). Go picks
	// CaptureAt from both (see contentTime).
	NSNumber *disp = info[SCStreamFrameInfoDisplayTime];
	uint64_t dispNs = disp != nil ? machToNs(disp.unsignedLongLongValue) : 0;
	mmcapGoFrame(self.handle, (uintptr_t)pb, dispNs, machToNs(entry),
	             (int)CVPixelBufferGetWidth(pb), (int)CVPixelBufferGetHeight(pb));
}

// The system stopped the stream (it does so on its own, e.g. around a
// screenshot): Go restarts it, off this queue.
- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
	if (self.stopped) {
		return;
	}
	NSString *msg = [NSString stringWithFormat:@"%@ (%@ %ld)", error.localizedDescription ?: @"unknown error",
	                                           error.domain ?: @"", (long)error.code];
	mmcapGoStopped(self.handle, (char *)msg.UTF8String);
}

@end

static BOOL nameMatches(NSString *name, NSString *want) {
	return name.length > 0 && [name caseInsensitiveCompare:want] == NSOrderedSame;
}

static BOOL appMatches(SCRunningApplication *app, NSString *want) API_AVAILABLE(macos(12.3)) {
	if (nameMatches(app.applicationName, want)) {
		return YES;
	}
	char buf[2 * MAXCOMLEN + 1] = {0};
	if (proc_name(app.processID, buf, sizeof(buf)) > 0 &&
	    nameMatches([NSString stringWithUTF8String:buf], want)) {
		return YES;
	}
	return NO;
}

void *mmcap_start(int64_t intervalNs, int maxWidth, const char *excludeApp,
                  uintptr_t handle, int *outW, int *outH, char **err) {
	@autoreleasepool {
		if (@available(macOS 12.3, *)) {
		} else {
			*err = dupErr(@"capture: ScreenCaptureKit requires macOS 12.3 or later");
			return NULL;
		}
		if (!CGPreflightScreenCaptureAccess()) {
			*err = dupErr(@"capture: Screen Recording permission is not granted "
			              "(System Settings > Privacy & Security > Screen & System Audio Recording)");
			return NULL;
		}

		__block SCShareableContent *content = nil;
		__block NSError *contentErr = nil;
		dispatch_semaphore_t sem = dispatch_semaphore_create(0);
		[SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *c, NSError *e) {
			content = c;
			contentErr = e;
			dispatch_semaphore_signal(sem);
		}];
		if (!waitSem(sem)) {
			*err = dupErr(@"capture: timed out getting shareable content");
			return NULL;
		}
		if (content == nil) {
			*err = dupErr([NSString stringWithFormat:@"capture: shareable content: %@",
			               contentErr.localizedDescription ?: @"unknown error"]);
			return NULL;
		}

		CGDirectDisplayID mainID = CGMainDisplayID();
		SCDisplay *display = nil;
		for (SCDisplay *d in content.displays) {
			if (d.displayID == mainID) {
				display = d;
				break;
			}
		}
		if (display == nil) {
			display = content.displays.firstObject;
		}
		if (display == nil) {
			*err = dupErr(@"capture: no display available");
			return NULL;
		}

		NSMutableArray<SCRunningApplication *> *excluded = [NSMutableArray array];
		NSString *want = excludeApp != NULL ? [NSString stringWithUTF8String:excludeApp] : @"";
		if (want.length > 0) {
			for (SCRunningApplication *app in content.applications) {
				if (appMatches(app, want)) {
					[excluded addObject:app];
				}
			}
		}
		SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:display
		                                             excludingApplications:excluded
		                                                  exceptingWindows:@[]];

		size_t pw = 0, ph = 0;
		CGDisplayModeRef mode = CGDisplayCopyDisplayMode(display.displayID);
		if (mode != NULL) {
			pw = CGDisplayModeGetPixelWidth(mode);
			ph = CGDisplayModeGetPixelHeight(mode);
			CGDisplayModeRelease(mode);
		}
		if (pw == 0 || ph == 0) {
			pw = (size_t)display.width;
			ph = (size_t)display.height;
		}
		int w = 0, h = 0;
		mmcapScaledSize((int)pw, (int)ph, maxWidth, &w, &h);

		SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
		cfg.width = (size_t)w;
		cfg.height = (size_t)h;
		cfg.pixelFormat = kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange;
		cfg.minimumFrameInterval = CMTimeMake(intervalNs, 1000000000);
		cfg.queueDepth = 3;
		// The pointer is not composited (like the Windows host): the viewer
		// shows its own, which follows the hand without the stream's latency;
		// drawing the host's too gives two pointers on the viewer.
		cfg.showsCursor = NO;
		cfg.scalesToFit = YES;
		cfg.colorSpaceName = kCGColorSpaceSRGB;
		cfg.colorMatrix = kCGDisplayStreamYCbCrMatrix_ITU_R_709_2;

		MMCapture *cap = [[MMCapture alloc] init];
		cap.handle = handle;
		cap.config = cfg;
		cap.queue = dispatch_queue_create("kkdesk.capture", DISPATCH_QUEUE_SERIAL);
		dispatch_queue_set_specific(cap.queue, kQueueKey, (__bridge void *)cap, NULL);
		cap.stream = [[SCStream alloc] initWithFilter:filter configuration:cfg delegate:cap];

		NSError *addErr = nil;
		if (![cap.stream addStreamOutput:cap type:SCStreamOutputTypeScreen sampleHandlerQueue:cap.queue error:&addErr]) {
			*err = dupErr([NSString stringWithFormat:@"capture: add stream output: %@", addErr.localizedDescription]);
			return NULL;
		}

		__block NSError *startErr = nil;
		dispatch_semaphore_t startSem = dispatch_semaphore_create(0);
		[cap.stream startCaptureWithCompletionHandler:^(NSError *e) {
			startErr = e;
			dispatch_semaphore_signal(startSem);
		}];
		if (!waitSem(startSem)) {
			cap.stopped = YES;
			*err = dupErr(@"capture: timed out starting stream");
			mmcap_stop((__bridge_retained void *)cap);
			return NULL;
		}
		if (startErr != nil) {
			cap.stopped = YES;
			*err = dupErr([NSString stringWithFormat:@"capture: start stream: %@", startErr.localizedDescription]);
			return NULL;
		}

		*outW = w;
		*outH = h;
		return (__bridge_retained void *)cap;
	}
}

void mmcap_set_fps(void *s, int64_t intervalNs) {
	if (@available(macOS 12.3, *)) {
		@autoreleasepool {
			MMCapture *cap = (__bridge MMCapture *)s;
			if (cap.stopped) {
				return;
			}
			cap.config.minimumFrameInterval = CMTimeMake(intervalNs, 1000000000);
			dispatch_semaphore_t sem = dispatch_semaphore_create(0);
			[cap.stream updateConfiguration:cap.config completionHandler:^(NSError *e) {
				if (e != nil) {
					NSLog(@"kkdesk capture: update configuration: %@", e.localizedDescription);
				}
				dispatch_semaphore_signal(sem);
			}];
			waitSem(sem);
		}
	}
}

void mmcap_stop(void *s) {
	if (@available(macOS 12.3, *)) {
		@autoreleasepool {
			MMCapture *cap = (__bridge_transfer MMCapture *)s;
			cap.stopped = YES;
			dispatch_semaphore_t sem = dispatch_semaphore_create(0);
			[cap.stream stopCaptureWithCompletionHandler:^(NSError *e) {
				dispatch_semaphore_signal(sem);
			}];
			waitSem(sem);
			[cap.stream removeStreamOutput:cap type:SCStreamOutputTypeScreen error:nil];
			// Drain the sample queue so no callback is running once we
			// return; skip when called from that queue (inside a callback).
			if (dispatch_get_specific(kQueueKey) != (__bridge void *)cap) {
				dispatch_sync(cap.queue, ^{});
			}
			cap.stream = nil;
		}
	}
}

int mmcap_has_permission(void) { return CGPreflightScreenCaptureAccess() ? 1 : 0; }

void mmcap_request_permission(void) { CGRequestScreenCaptureAccess(); }

uint32_t mmcap_pixel_format(uintptr_t pixelBuffer) {
	if (pixelBuffer == 0) {
		return 0;
	}
	return (uint32_t)CVPixelBufferGetPixelFormatType((CVPixelBufferRef)pixelBuffer);
}
