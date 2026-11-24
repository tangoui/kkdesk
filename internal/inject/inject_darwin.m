//go:build darwin && cgo

// CGEvent injection for package inject. All functions are thread-safe; the
// Go side serializes Apply so the posted order matches the received order.

#include "inject_darwin.h"

#import <AppKit/AppKit.h>
#include <ApplicationServices/ApplicationServices.h>
#include <CoreGraphics/CoreGraphics.h>

int mminj_has_permission(void) { return AXIsProcessTrusted() ? 1 : 0; }

void mminj_request_permission(void) {
	NSDictionary *opts = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt : @YES};
	AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)opts);
}

void mminj_main_bounds(double *x, double *y, double *w, double *h) {
	CGRect r = CGDisplayBounds(CGMainDisplayID());
	*x = r.origin.x;
	*y = r.origin.y;
	*w = r.size.width;
	*h = r.size.height;
}

double mminj_double_click_interval(void) { return [NSEvent doubleClickInterval]; }

int mminj_mouse(int type, double x, double y, int button, int64_t clickState, uint64_t flags) {
	CGEventRef ev = CGEventCreateMouseEvent(NULL, (CGEventType)type, CGPointMake(x, y), (CGMouseButton)button);
	if (ev == NULL) return -1;
	if (clickState > 0) CGEventSetIntegerValueField(ev, kCGMouseEventClickState, clickState);
	CGEventSetFlags(ev, (CGEventFlags)flags);
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
	return 0;
}

int mminj_key(uint16_t keycode, int down, int flagsChanged, uint64_t flags) {
	CGEventRef ev = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, down ? true : false);
	if (ev == NULL) return -1;
	// Real modifier keys produce kCGEventFlagsChanged, not key down/up.
	if (flagsChanged) CGEventSetType(ev, kCGEventFlagsChanged);
	CGEventSetFlags(ev, (CGEventFlags)flags);
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
	return 0;
}

int mminj_scroll(int32_t dx, int32_t dy, uint64_t flags) {
	CGEventRef ev = CGEventCreateScrollWheelEvent2(NULL, kCGScrollEventUnitPixel, 2, dy, dx, 0);
	if (ev == NULL) return -1;
	CGEventSetFlags(ev, (CGEventFlags)flags);
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
	return 0;
}
