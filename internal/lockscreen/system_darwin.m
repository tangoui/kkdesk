//go:build darwin && cgo

#import <CoreGraphics/CoreGraphics.h>
#import <CoreVideo/CoreVideo.h>
#import <Foundation/Foundation.h>
#import <IOKit/IOKitLib.h>
#import <Security/Security.h>
#import <ServiceManagement/ServiceManagement.h>
#import <SystemConfiguration/SystemConfiguration.h>
#include <bsm/libbsm.h>
#include <errno.h>
#include <fcntl.h>
#include <sys/mman.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

#include "system_darwin.h"

static int identity(SecCodeRef code, kk_mac_identity *out) {
    SecRequirementRef requirement = NULL;
    if (SecRequirementCreateWithString(CFSTR("anchor apple generic"), kSecCSDefaultFlags, &requirement) != errSecSuccess)
        return 0;
    OSStatus verified = SecCodeCheckValidity(code, kSecCSDefaultFlags, requirement);
    CFRelease(requirement);
    if (verified != errSecSuccess) return 0;
    SecStaticCodeRef image = NULL;
    if (SecCodeCopyStaticCode(code, kSecCSDefaultFlags, &image) != errSecSuccess) return 0;
    CFDictionaryRef info = NULL;
    OSStatus found = SecCodeCopySigningInformation(image, kSecCSSigningInformation, &info);
    CFRelease(image);
    if (found != errSecSuccess) return 0;
    CFStringRef team = CFDictionaryGetValue(info, kSecCodeInfoTeamIdentifier);
    CFStringRef identifier = CFDictionaryGetValue(info, kSecCodeInfoIdentifier);
    int ok = team && identifier && CFStringGetLength(team) > 0 && CFStringGetLength(identifier) > 0 &&
        CFStringGetCString(team, out->team, sizeof(out->team), kCFStringEncodingUTF8) &&
        CFStringGetCString(identifier, out->identifier, sizeof(out->identifier), kCFStringEncodingUTF8);
    CFRelease(info);
    return ok;
}

int kk_mac_self(kk_mac_identity *out) {
    @autoreleasepool {
        SecCodeRef self = NULL;
        if (SecCodeCopySelf(kSecCSDefaultFlags, &self) != errSecSuccess) return 0;
        int ok = identity(self, out);
        CFRelease(self);
        out->uid = geteuid();
        return ok;
    }
}

int kk_mac_peer(int fd, kk_mac_identity *out) {
    @autoreleasepool {
        uid_t uid;
        gid_t group;
        if (getpeereid(fd, &uid, &group) != 0) return 0;
        audit_token_t token;
        socklen_t length = sizeof(token);
        if (getsockopt(fd, SOL_LOCAL, LOCAL_PEERTOKEN, &token, &length) != 0 || length != sizeof(token) ||
            audit_token_to_euid(token) != uid) return 0;
        NSData *audit = [NSData dataWithBytes:&token length:sizeof(token)];
        NSDictionary *attributes = @{(__bridge NSString *)kSecGuestAttributeAudit: audit};
        SecCodeRef guest = NULL;
        if (SecCodeCopyGuestWithAttributes(NULL, (__bridge CFDictionaryRef)attributes, kSecCSDefaultFlags, &guest) != errSecSuccess)
            return 0;
        int ok = identity(guest, out);
        CFRelease(guest);
        out->uid = uid;
        return ok;
    }
}

int kk_mac_console(uint32_t *uid) {
    @autoreleasepool {
        uid_t user = (uid_t)-1;
        NSString *name = CFBridgingRelease(SCDynamicStoreCopyConsoleUser(NULL, &user, NULL));
        if (!name || [name isEqualToString:@"loginwindow"] || !user || user == (uid_t)-1) return 0;
        *uid = user;
        return 1;
    }
}

int kk_mac_console_locked(uint32_t uid) {
    @autoreleasepool {
        uint32_t current;
        if (!kk_mac_console(&current) || current != uid) return 0;
        NSDictionary *session = CFBridgingRelease(CGSessionCopyCurrentDictionary());
        NSNumber *user = session[(__bridge NSString *)kCGSessionUserIDKey];
        if (user && user.unsignedIntValue == uid && [session[(__bridge NSString *)kCGSessionOnConsoleKey] boolValue]) {
            NSNumber *locked = session[@"CGSSessionScreenIsLocked"];
            return [locked isKindOfClass:NSNumber.class] && locked.boolValue;
        }
        io_registry_entry_t root = IORegistryGetRootEntry(kIOMainPortDefault);
        if (!root) return 0;
        id users = CFBridgingRelease(IORegistryEntryCreateCFProperty(root, CFSTR("IOConsoleUsers"), NULL, 0));
        IOObjectRelease(root);
        if (![users isKindOfClass:NSArray.class]) return 0;
        for (id entry in users) {
            if (![entry isKindOfClass:NSDictionary.class]) continue;
            NSNumber *account = entry[(__bridge NSString *)kCGSessionUserIDKey];
            NSNumber *active = entry[(__bridge NSString *)kCGSessionOnConsoleKey];
            NSNumber *locked = entry[@"CGSSessionScreenIsLocked"];
            if ([account isKindOfClass:NSNumber.class] && account.unsignedIntValue == uid &&
                [active isKindOfClass:NSNumber.class] && active.boolValue) {
                return [locked isKindOfClass:NSNumber.class] && locked.boolValue;
            }
        }
        return 0;
    }
}

int kk_mac_job_status(int agent) {
    @autoreleasepool {
        if (@available(macOS 13.0, *)) {
            SMAppService *job = agent ? [SMAppService agentServiceWithPlistName:@"com.kkdesk.lockscreen.agent.plist"]
                : [SMAppService daemonServiceWithPlistName:@"com.kkdesk.lockscreen.daemon.plist"];
            return (int)job.status;
        }
        return -1;
    }
}

int kk_mac_job_change(int agent, int install) {
    @autoreleasepool {
        if (@available(macOS 13.0, *)) {
            SMAppService *job = agent ? [SMAppService agentServiceWithPlistName:@"com.kkdesk.lockscreen.agent.plist"]
                : [SMAppService daemonServiceWithPlistName:@"com.kkdesk.lockscreen.daemon.plist"];
            NSError *error = nil;
            BOOL ok = install ? [job registerAndReturnError:&error] : [job unregisterAndReturnError:&error];
            return ok ? 0 : (int)(error ? error.code : EIO);
        }
        return ENOTSUP;
    }
}

int kk_mac_shm(const char *name, size_t size, int *writer, int *reader) {
    int w = shm_open(name, O_RDWR | O_CREAT | O_EXCL, 0600);
    if (w < 0) return errno;
    int r = -1;
    int err = 0;
    if (ftruncate(w, (off_t)size) != 0) err = errno;
    if (!err && (r = shm_open(name, O_RDONLY, 0)) < 0) err = errno;
    if (shm_unlink(name) != 0 && !err) err = errno;
    if (err) {
        close(w);
        if (r >= 0) close(r);
        return err;
    }
    *writer = w;
    *reader = r;
    return 0;
}

int kk_mac_inject_password(const uint16_t *password, size_t size) {
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, 0, true);
    CGEventRef up = CGEventCreateKeyboardEvent(NULL, 0, false);
    if (!down || !up) {
        if (down) CFRelease(down);
        if (up) CFRelease(up);
        return 0;
    }
    CGEventSetFlags(down, 0);
    CGEventSetFlags(up, 0);
    CGEventKeyboardSetUnicodeString(down, size, password);
    CGEventPost(kCGHIDEventTap, down);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(down);
    CFRelease(up);
    return 1;
}

static int nv12_copy(CVPixelBufferRef p, uint8_t *pixels, size_t size, size_t w, size_t h, int read) {
    if (!p || size != w * h * 3 / 2 || CVPixelBufferGetPixelFormatType(p) != kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange ||
        CVPixelBufferGetPlaneCount(p) != 2 || CVPixelBufferGetWidth(p) != w || CVPixelBufferGetHeight(p) != h)
        return 0;
    CVPixelBufferLockFlags flags = read ? kCVPixelBufferLock_ReadOnly : 0;
    if (CVPixelBufferLockBaseAddress(p, flags) != kCVReturnSuccess) return 0;
    int ok = 1;
    for (size_t plane = 0; plane < 2; plane++) {
        size_t rows = plane == 0 ? h : h / 2;
        size_t stride = CVPixelBufferGetBytesPerRowOfPlane(p, plane);
        uint8_t *base = CVPixelBufferGetBaseAddressOfPlane(p, plane);
        if (!base || stride < w || CVPixelBufferGetHeightOfPlane(p, plane) != rows) {
            ok = 0;
            break;
        }
        for (size_t y = 0; y < rows; y++) {
            if (read) memcpy(pixels, base + y * stride, w);
            else memcpy(base + y * stride, pixels, w);
            pixels += w;
        }
    }
    CVPixelBufferUnlockBaseAddress(p, flags);
    return ok;
}

int kk_mac_read_nv12(uintptr_t pixel, uint8_t *dst, size_t size, uint32_t w, uint32_t h) {
    return nv12_copy((CVPixelBufferRef)pixel, dst, size, w, h, 1);
}

uintptr_t kk_mac_make_nv12(const uint8_t *src, size_t size, uint32_t w, uint32_t h) {
    @autoreleasepool {
        NSDictionary *attrs = @{(__bridge NSString *)kCVPixelBufferIOSurfacePropertiesKey: @{}};
        CVPixelBufferRef pixel = NULL;
        if (CVPixelBufferCreate(NULL, w, h, kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange,
            (__bridge CFDictionaryRef)attrs, &pixel) != kCVReturnSuccess) return 0;
        if (!nv12_copy(pixel, (uint8_t *)src, size, w, h, 0)) {
            CVPixelBufferRelease(pixel);
            return 0;
        }
        return (uintptr_t)pixel;
    }
}

void kk_mac_release_nv12(uintptr_t pixel) { CVPixelBufferRelease((CVPixelBufferRef)pixel); }
