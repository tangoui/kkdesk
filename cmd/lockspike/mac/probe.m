#import <CoreGraphics/CoreGraphics.h>
#import <CoreImage/CoreImage.h>
#import <Foundation/Foundation.h>
#import <IOKit/IOKitLib.h>
#import <ImageIO/ImageIO.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <Security/AuthSession.h>
#import <ServiceManagement/ServiceManagement.h>
#include <dlfcn.h>
#include <sys/stat.h>
#include <unistd.h>

typedef CGImageRef (*WindowImageFn)(CGRect, CGWindowListOption, CGWindowID, CGWindowImageOption);
typedef CGDisplayStreamRef (*DisplayStreamFn)(CGDirectDisplayID, size_t, size_t, int32_t,
    CFDictionaryRef, dispatch_queue_t, CGDisplayStreamFrameAvailableHandler);
typedef CGError (*StreamCommandFn)(CGDisplayStreamRef);

static NSString *const lockKey = @"CGSSessionScreenIsLocked";

static void report(NSString *format, ...) NS_FORMAT_FUNCTION(1, 2);
static void report(NSString *format, ...) {
    va_list args;
    va_start(args, format);
    NSString *message = [[NSString alloc] initWithFormat:format arguments:args];
    va_end(args);
    fprintf(stdout, "%s %s\n", NSDate.date.description.UTF8String, message.UTF8String);
    fflush(stdout);
}

static NSDictionary *session(void) {
    return CFBridgingRelease(CGSessionCopyCurrentDictionary());
}

static NSNumber *consoleLock(void) {
    io_registry_entry_t root = IORegistryGetRootEntry(kIOMainPortDefault);
    if (!root) return nil;
    NSArray *users = CFBridgingRelease(IORegistryEntryCreateCFProperty(
        root, CFSTR("IOConsoleUsers"), kCFAllocatorDefault, 0));
    IOObjectRelease(root);
    for (NSDictionary *user in users) {
        if ([user[@"kCGSSessionOnConsoleKey"] boolValue]) return user[lockKey];
    }
    return nil;
}

static BOOL locked(void) {
    NSDictionary *current = session();
    return ([current[(NSString *)kCGSessionOnConsoleKey] boolValue] && [current[lockKey] boolValue]) ||
        [consoleLock() boolValue];
}

static NSString *managerName(void) {
    NSPipe *output = [NSPipe pipe];
    NSTask *task = [NSTask new];
    task.executableURL = [NSURL fileURLWithPath:@"/bin/launchctl"];
    task.arguments = @[@"managername"];
    task.standardOutput = output;
    task.standardError = [NSFileHandle fileHandleWithNullDevice];
    if (![task launchAndReturnError:NULL]) return @"unknown";
    NSData *bytes = [output.fileHandleForReading readDataToEndOfFile];
    [task waitUntilExit];
    NSString *name = [[NSString alloc] initWithData:bytes encoding:NSUTF8StringEncoding];
    return task.terminationStatus == 0 ? [name stringByTrimmingCharactersInSet:
        NSCharacterSet.whitespaceAndNewlineCharacterSet] : @"unknown";
}

static void inspect(NSString *mode) {
    NSDictionary *current = session();
    SecuritySessionId asid = 0;
    SessionAttributeBits flags = 0;
    OSStatus status = SessionGetInfo(callerSecuritySession, &asid, &flags);
    report(@"mode=%@ pid=%d uid=%d euid=%d manager=%@ auditStatus=%d asid=%u auditFlags=0x%x mainDisplay=%u",
        mode, getpid(), getuid(), geteuid(), managerName(), status, asid, flags, CGMainDisplayID());
    report(@"sessionPresent=%d sessionOnConsole=%@ sessionLocked=%@ consoleLocked=%@",
        current != nil, current[(NSString *)kCGSessionOnConsoleKey], current[lockKey], consoleLock());
    report(@"CGWindowListCreateImage symbol=%d CGDisplayStreamCreateWithDispatchQueue symbol=%d",
        dlsym(RTLD_DEFAULT, "CGWindowListCreateImage") != NULL,
        dlsym(RTLD_DEFAULT, "CGDisplayStreamCreateWithDispatchQueue") != NULL);
}

static BOOL saveImage(CGImageRef image, NSString *path, NSString *method) {
    if (!image) {
        report(@"%@ no image", method);
        return NO;
    }
    NSURL *url = [NSURL fileURLWithPath:path];
    CGImageDestinationRef dest = CGImageDestinationCreateWithURL(
        (__bridge CFURLRef)url, CFSTR("public.png"), 1, NULL);
    if (!dest) {
        report(@"%@ could not open PNG destination", method);
        return NO;
    }
    CGImageDestinationAddImage(dest, image, NULL);
    BOOL ok = CGImageDestinationFinalize(dest);
    CFRelease(dest);
    report(@"%@ png=%d size=%zux%zu file=%@", method, ok,
        CGImageGetWidth(image), CGImageGetHeight(image), path.lastPathComponent);
    return ok;
}

// Callbacks only provide pixels. The caller decides whether the machine is still locked.
static void captureSCK(NSString *path) {
    CGDirectDisplayID primary = CGMainDisplayID();
    if (!primary) {
        report(@"ScreenCaptureKit no main display");
        return;
    }
    dispatch_semaphore_t ready = dispatch_semaphore_create(0);
    __block SCShareableContent *content;
    __block NSError *error;
    [SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *c, NSError *e) {
        content = c;
        error = e;
        dispatch_semaphore_signal(ready);
    }];
    if (dispatch_semaphore_wait(ready, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC))) {
        report(@"ScreenCaptureKit shareable content timeout");
        return;
    }
    SCDisplay *display = nil;
    for (SCDisplay *candidate in content.displays) {
        if (candidate.displayID == primary) {
            display = candidate;
            break;
        }
    }
    if (!display) {
        report(@"ScreenCaptureKit no matching display count=%lu error=%@", content.displays.count, error);
        return;
    }
    if (!locked()) return;
    SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];
    SCStreamConfiguration *config = [SCStreamConfiguration new];
    config.width = CGDisplayPixelsWide(primary);
    config.height = CGDisplayPixelsHigh(primary);
    config.showsCursor = NO;
    __block CGImageRef image = NULL;
    __block BOOL finished = NO;
    NSLock *guard = [NSLock new];
    [SCScreenshotManager captureImageWithFilter:filter configuration:config
        completionHandler:^(CGImageRef result, NSError *e) {
            [guard lock];
            if (!finished) {
                image = result ? CGImageRetain(result) : NULL;
                if (e) report(@"ScreenCaptureKit capture error=%@", e);
                dispatch_semaphore_signal(ready);
            }
            [guard unlock];
        }];
    BOOL timedOut = dispatch_semaphore_wait(ready, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC)) != 0;
    [guard lock];
    finished = YES;
    if (timedOut) report(@"ScreenCaptureKit image timeout");
    if (!timedOut && locked()) saveImage(image, path, @"ScreenCaptureKit");
    if (image) CGImageRelease(image);
    [guard unlock];
}

static void captureDisplayStream(NSString *path) {
    DisplayStreamFn create = (DisplayStreamFn)dlsym(RTLD_DEFAULT, "CGDisplayStreamCreateWithDispatchQueue");
    StreamCommandFn start = (StreamCommandFn)dlsym(RTLD_DEFAULT, "CGDisplayStreamStart");
    StreamCommandFn stop = (StreamCommandFn)dlsym(RTLD_DEFAULT, "CGDisplayStreamStop");
    if (!create || !start || !stop) {
        report(@"CGDisplayStream runtime symbols missing");
        return;
    }
    CGDirectDisplayID display = CGMainDisplayID();
    if (!display) {
        report(@"CGDisplayStream no main display");
        return;
    }
    dispatch_queue_t queue = dispatch_queue_create("com.kkdesk.lockspike.displaystream", DISPATCH_QUEUE_SERIAL);
    dispatch_semaphore_t ready = dispatch_semaphore_create(0);
    __block CGImageRef image = NULL;
    __block BOOL finished = NO;
    CGDisplayStreamRef stream = create(display, CGDisplayPixelsWide(display), CGDisplayPixelsHigh(display),
        kCVPixelFormatType_32BGRA, NULL, queue,
        ^(CGDisplayStreamFrameStatus status, uint64_t time, IOSurfaceRef surface, CGDisplayStreamUpdateRef update) {
            if (finished) return;
            report(@"CGDisplayStream frame status=%d surface=%d", status, surface != NULL);
            if (status == kCGDisplayStreamFrameStatusFrameComplete && surface) {
                CIImage *source = [CIImage imageWithIOSurface:surface options:nil];
                image = [[CIContext context] createCGImage:source fromRect:source.extent];
                finished = YES;
                dispatch_semaphore_signal(ready);
            }
        });
    if (!stream) {
        report(@"CGDisplayStream create returned NULL");
        return;
    }
    CGError result = start(stream);
    report(@"CGDisplayStream start=%d", result);
    if (result == kCGErrorSuccess) {
        if (dispatch_semaphore_wait(ready, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC))) {
            report(@"CGDisplayStream complete frame timeout");
        }
        stop(stream);
    }
    dispatch_sync(queue, ^{
        finished = YES;
        if (locked()) saveImage(image, path, @"CGDisplayStream");
        if (image) CGImageRelease(image);
    });
    CFRelease(stream);
}

static void captureWindowList(NSString *path) {
    WindowImageFn create = (WindowImageFn)dlsym(RTLD_DEFAULT, "CGWindowListCreateImage");
    if (!create) {
        report(@"CGWindowListCreateImage runtime symbol missing");
        return;
    }
    CGDirectDisplayID display = CGMainDisplayID();
    if (!display) {
        report(@"CGWindowListCreateImage no main display");
        return;
    }
    CGImageRef image = create(CGDisplayBounds(display), kCGWindowListOptionOnScreenOnly,
        kCGNullWindowID, kCGWindowImageDefault);
    if (locked()) saveImage(image, path, @"CGWindowListCreateImage");
    if (image) CGImageRelease(image);
}

static void capture(NSString *directory, NSString *stage) {
    if (!locked()) return;
    dispatch_group_t group = dispatch_group_create();
    dispatch_queue_t queue = dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0);
    dispatch_group_async(group, queue, ^{
        if (locked()) captureSCK([directory stringByAppendingPathComponent:
            [stage stringByAppendingString:@"-sck.png"]]);
    });
    dispatch_group_async(group, queue, ^{
        if (locked()) captureDisplayStream([directory stringByAppendingPathComponent:
            [stage stringByAppendingString:@"-displaystream.png"]]);
    });
    dispatch_group_async(group, queue, ^{
        if (locked()) captureWindowList([directory stringByAppendingPathComponent:
            [stage stringByAppendingString:@"-windowlist.png"]]);
    });
    while (dispatch_group_wait(group, dispatch_time(DISPATCH_TIME_NOW, 100 * NSEC_PER_MSEC))) {
        CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.02, false);
    }
}

static void postLetters(CGEventTapLocation tap) {
    unsigned posted = 0;
    const CGKeyCode keys[] = {0, 11, 8}; // a, b, c on the US layout
    for (unsigned i = 0; i < sizeof(keys) / sizeof(keys[0]); ++i) {
        if (!locked()) break;
        CGEventRef down = CGEventCreateKeyboardEvent(NULL, keys[i], true);
        CGEventRef up = CGEventCreateKeyboardEvent(NULL, keys[i], false);
        if (down && up) {
            CGEventSetFlags(down, 0);
            CGEventSetFlags(up, 0);
            CGEventPost(tap, down);
            CGEventPost(tap, up);
            ++posted;
        }
        if (down) CFRelease(down);
        if (up) CFRelease(up);
        usleep(125000);
    }
    report(@"CGEventPost tap=%u postedPairs=%u (no delivery result)", tap, posted);
}

static int watch(NSString *mode, NSString *tap, NSString *directory, NSString *expires) {
    NSDictionary *taps = @{@"hid": @(kCGHIDEventTap), @"session": @(kCGSessionEventTap),
        @"annotated": @(kCGAnnotatedSessionEventTap), @"none": @(-1)};
    if (!taps[tap] || expires.doubleValue < NSDate.date.timeIntervalSince1970) return 2;
    inspect(mode);
    if ([mode isEqualToString:@"loginwindow"] && ![managerName() isEqualToString:@"LoginWindow"]) {
        report(@"process did not start in a LoginWindow bootstrap");
        return 1;
    }
    report(@"waiting for lock; tap=%@", tap);
    while (!locked() && NSDate.date.timeIntervalSince1970 < expires.doubleValue) usleep(250000);
    if (!locked() || NSDate.date.timeIntervalSince1970 >= expires.doubleValue) {
        report(@"no positive lock state before deadline");
        return 1;
    }
    sleep(4);
    if (!locked()) {
        report(@"unlock before probe");
        return 1;
    }
    inspect(mode);
    BOOL screen = CGPreflightScreenCaptureAccess();
    BOOL post = CGPreflightPostEventAccess();
    report(@"preflight screenCapture=%d postEvent=%d", screen, post);
    capture(directory, @"before");
    if (![tap isEqualToString:@"none"] && locked()) {
        postLetters((CGEventTapLocation)[taps[tap] unsignedIntValue]);
        sleep(1);
        capture(directory, @"after");
    }
    report(@"done locked=%d", locked());
    return 0;
}

static int service(NSString *action, NSString *kind) {
    SMAppService *job;
    if ([kind isEqualToString:@"daemon"]) {
        job = [SMAppService daemonServiceWithPlistName:@"com.kkdesk.lockspike.sm.daemon.plist"];
    } else if ([kind isEqualToString:@"agent"]) {
        job = [SMAppService agentServiceWithPlistName:@"com.kkdesk.lockspike.sm.agent.plist"];
    } else if ([kind isEqualToString:@"loginwindow"]) {
        job = [SMAppService agentServiceWithPlistName:@"com.kkdesk.lockspike.sm.loginwindow.plist"];
    } else {
        return 2;
    }
    NSError *error = nil;
    BOOL ok = YES;
    if ([action isEqualToString:@"register"]) ok = [job registerAndReturnError:&error];
    else if ([action isEqualToString:@"unregister"]) ok = [job unregisterAndReturnError:&error];
    else if (![action isEqualToString:@"status"]) return 2;
    static NSString *const statuses[] = {@"notRegistered", @"enabled", @"requiresApproval", @"notFound"};
    NSUInteger status = job.status;
    report(@"SMAppService kind=%@ action=%@ ok=%d status=%lu (%@) error=%@", kind, action, ok,
        status, status < sizeof(statuses) / sizeof(statuses[0]) ? statuses[status] : @"unknown", error);
    return ok ? 0 : 1;
}

int main(int argc, const char **argv) {
    @autoreleasepool {
        umask(0077);
        if (argc == 2 && !strcmp(argv[1], "--inspect")) {
            inspect(@"inspect");
            return 0;
        }
        if (argc == 3 && !strcmp(argv[1], "--sm-marker")) {
            inspect(@(argv[2]));
            return 0;
        }
        if (argc == 4 && !strcmp(argv[1], "--sm")) return service(@(argv[2]), @(argv[3]));
        if (argc == 6 && !strcmp(argv[1], "--watch")) {
            return watch(@(argv[2]), @(argv[3]), @(argv[4]), @(argv[5]));
        }
        fprintf(stderr, "usage: lockspike --inspect | --sm status|register|unregister daemon|agent|loginwindow\n");
        return 2;
    }
}
