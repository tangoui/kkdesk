//go:build cgo

package app

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics
#import <AppKit/AppKit.h>
#include <stdint.h>

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
static NSCursor *cursorForIndex(int index) {
	switch (index) {
	case -1: return NSCursor.currentSystemCursor;
	case 0: return NSCursor.arrowCursor;
	case 1: return NSCursor.pointingHandCursor;
	case 2: return NSCursor.IBeamCursor;
	case 3: return NSCursor.operationNotAllowedCursor;
	case 4: return NSCursor.resizeLeftRightCursor;
	case 5: return NSCursor.resizeUpDownCursor;
	case 6: return NSCursor.openHandCursor;
	case 7: return NSCursor.closedHandCursor;
	default: return nil;
	}
}
#pragma clang diagnostic pop

static int cursorFingerprint(int index, uint64_t *hash, int *x, int *y) {
	@autoreleasepool {
		NSCursor *cursor = cursorForIndex(index);
		NSImage *image = cursor.image;
		NSSize size = image.size;
		if (!image || size.width <= 0 || size.height <= 0) return NO;

		// The WindowServer and AppKit can supply different bitmap formats
		// for the same cursor. Hash the pixels in a common size and format.
		enum { side = 32, rowBytes = side * 4 };
		NSRect bounds = NSMakeRect(0, 0, size.width, size.height);
		CGImageRef bitmap = [image CGImageForProposedRect:&bounds context:nil hints:nil];
		if (!bitmap) return NO;
		uint8_t pixels[side * rowBytes] = {0};
		CGColorSpaceRef colorSpace = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
		if (!colorSpace) return NO;
		CGContextRef ctx = CGBitmapContextCreate(pixels, side, side, 8, rowBytes,
			colorSpace, kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
		CGColorSpaceRelease(colorSpace);
		if (!ctx) return NO;
		CGContextDrawImage(ctx, CGRectMake(0, 0, side, side), bitmap);
		CGContextRelease(ctx);

		uint64_t h = UINT64_C(14695981039346656037);
		for (size_t i = 0; i < sizeof(pixels); i++) {
			h = (h ^ pixels[i]) * UINT64_C(1099511628211);
		}
		*hash = h;
		*x = (int)lround(1024 * cursor.hotSpot.x / size.width);
		*y = (int)lround(1024 * cursor.hotSpot.y / size.height);
		return YES;
	}
}
*/
import "C"

import (
	"sync"

	"github.com/tangoui/kkdesk/internal/session"
)

type darwinCursorFingerprint struct {
	image uint64
	x, y  int
}

// Keep in the same order as cursorForIndex.
var darwinCursorShapes = [...]session.CursorShape{
	session.CursorDefault, session.CursorPointer, session.CursorText, session.CursorNotAllowed,
	session.CursorResizeEW, session.CursorResizeNS, session.CursorGrab, session.CursorGrabbing,
}

var darwinStandardCursors = sync.OnceValue(func() [len(darwinCursorShapes)]darwinCursorFingerprint {
	var standards [len(darwinCursorShapes)]darwinCursorFingerprint
	for i := range standards {
		standards[i], _ = readDarwinCursor(i)
	}
	return standards
})

func readDarwinCursor(index int) (darwinCursorFingerprint, bool) {
	var hash C.uint64_t
	var x, y C.int
	ok := C.cursorFingerprint(C.int(index), &hash, &x, &y)
	return darwinCursorFingerprint{image: uint64(hash), x: int(x), y: int(y)}, ok != 0
}

func matchDarwinCursor(current darwinCursorFingerprint, standards [len(darwinCursorShapes)]darwinCursorFingerprint) session.CursorShape {
	for i, standard := range standards {
		if standard == current {
			return darwinCursorShapes[i]
		}
	}
	return session.CursorDefault
}

func hostCursor() (session.CursorShape, bool) {
	current, ok := readDarwinCursor(-1)
	if !ok {
		return session.CursorDefault, false
	}
	return matchDarwinCursor(current, darwinStandardCursors()), true
}
