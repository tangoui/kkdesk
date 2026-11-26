//go:build darwin && cgo

#import <AppKit/AppKit.h>
#import <Carbon/Carbon.h>
#include "privacy_darwin.h"
#include "_cgo_export.h"

@interface PrivacyPanel : NSPanel
@property(nonatomic, strong) NSTextField *message;
@end

@implementation PrivacyPanel
- (BOOL)canBecomeKeyWindow { return NO; }
- (BOOL)canBecomeMainWindow { return NO; }
@end

@interface PrivacyScreen : NSObject
@property(nonatomic) uintptr_t handle;
@property(nonatomic) EventHotKeyRef hotkey;
@property(nonatomic) EventHandlerRef hotkeyHandler;
@property(nonatomic, strong) id displayObserver;
@property(nonatomic, strong) NSMutableDictionary<NSNumber *, PrivacyPanel *> *panels;
@property(nonatomic) int countdown;
@end

@implementation PrivacyScreen
@end

static void onMain(dispatch_block_t action) {
    if (NSThread.isMainThread) {
        action();
    } else {
        dispatch_sync(dispatch_get_main_queue(), action);
    }
}

static OSStatus onHotkey(EventHandlerCallRef next, EventRef event, void *context) {
    EventHotKeyID id = {0};
    if (GetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID,
                          NULL, sizeof(id), NULL, &id) != noErr ||
        id.signature != 'kkpr' || id.id != 1) {
        return eventNotHandledErr;
    }
    PrivacyScreen *screen = (__bridge PrivacyScreen *)context;
    mmprivacyDarwinEvent(screen.handle, 0);
    return noErr;
}

void *mmprivacy_new(uintptr_t handle, int *status) {
    __block void *result = NULL;
    onMain(^{
        @autoreleasepool {
            PrivacyScreen *screen = [PrivacyScreen new];
            screen.handle = handle;
            screen.countdown = -1;
            screen.panels = [NSMutableDictionary dictionary];
            EventTypeSpec spec = {kEventClassKeyboard, kEventHotKeyPressed};
            EventHandlerRef handler = NULL;
            *status = InstallApplicationEventHandler(onHotkey, 1, &spec, (__bridge void *)screen, &handler);
            if (*status != noErr) {
                return;
            }
            screen.hotkeyHandler = handler;
            EventHotKeyRef hotkey = NULL;
            EventHotKeyID id = {'kkpr', 1};
            *status = RegisterEventHotKey(kVK_ANSI_P, controlKey | optionKey | cmdKey,
                                         id, GetApplicationEventTarget(), 0, &hotkey);
            if (*status != noErr) {
                RemoveEventHandler(handler);
                return;
            }
            screen.hotkey = hotkey;
            screen.displayObserver = [NSNotificationCenter.defaultCenter
                addObserverForName:NSApplicationDidChangeScreenParametersNotification
                object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note) {
                    mmprivacyDarwinEvent(handle, 1);
                }];
            result = (__bridge_retained void *)screen;
        }
    });
    return result;
}

static PrivacyPanel *newPanel(NSRect frame) {
    PrivacyPanel *panel = [[PrivacyPanel alloc]
        initWithContentRect:frame
                  styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
                    backing:NSBackingStoreBuffered defer:NO];
    panel.level = NSScreenSaverWindowLevel;
    panel.ignoresMouseEvents = YES;
    panel.hidesOnDeactivate = NO;
    panel.animationBehavior = NSWindowAnimationBehaviorNone;
    panel.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces |
                               NSWindowCollectionBehaviorFullScreenAuxiliary |
                               NSWindowCollectionBehaviorStationary |
                               NSWindowCollectionBehaviorIgnoresCycle;
    // Make the window discoverable by ScreenCaptureKit before drawing
    // anything into it. The remote stream excludes it before mmprivacy_show.
    panel.opaque = NO;
    panel.alphaValue = 0.02;
    panel.backgroundColor = NSColor.clearColor;
    [panel orderFrontRegardless];
    return panel;
}

uint32_t *mmprivacy_prepare(void *ptr, size_t *count) {
    __block uint32_t *ids = NULL;
    onMain(^{
        @autoreleasepool {
            PrivacyScreen *screen = (__bridge PrivacyScreen *)ptr;
            NSArray<NSScreen *> *displays = NSScreen.screens;
            if (displays.count == 0) {
                return;
            }
            ids = calloc(displays.count, sizeof(*ids));
            if (ids == NULL) {
                return;
            }
            NSMutableSet<NSNumber *> *active = [NSMutableSet setWithCapacity:displays.count];
            for (NSScreen *display in displays) {
                NSNumber *key = display.deviceDescription[@"NSScreenNumber"];
                [active addObject:key];
                PrivacyPanel *panel = screen.panels[key];
                if (panel == nil) {
                    panel = newPanel(display.frame);
                    screen.panels[key] = panel;
                } else {
                    [panel setFrame:display.frame display:YES];
                }
                ids[(*count)++] = (uint32_t)panel.windowNumber;
            }
            for (NSNumber *key in screen.panels.allKeys) {
                if (![active containsObject:key]) {
                    [screen.panels[key] orderOut:nil];
                    [screen.panels removeObjectForKey:key];
                }
            }
        }
    });
    return ids;
}

static NSString *coverMessage(int countdown) {
    if (countdown < 0) return @"正在被远程控制，屏幕已隐藏。\n按 ⌃⌥⌘P 结束隐私屏";
    if (countdown == 0) return @"正在锁屏…";
    return [NSString stringWithFormat:@"连接已断开，%d 秒后锁屏\n按 ⌃⌥⌘P 取消锁屏并结束隐私屏", countdown];
}

void mmprivacy_show(void *ptr) {
    onMain(^{
        @autoreleasepool {
            PrivacyScreen *screen = (__bridge PrivacyScreen *)ptr;
            for (PrivacyPanel *panel in screen.panels.allValues) {
                if (panel.alphaValue > 0.5) {
                    continue;
                }
                panel.backgroundColor = NSColor.blackColor;
                panel.alphaValue = 254.0 / 255.0;
                NSTextField *label = [NSTextField wrappingLabelWithString:coverMessage(screen.countdown)];
                panel.message = label;
                label.alignment = NSTextAlignmentCenter;
                label.font = [NSFont systemFontOfSize:24];
                label.textColor = NSColor.whiteColor;
                label.translatesAutoresizingMaskIntoConstraints = NO;
                [panel.contentView addSubview:label];
                [NSLayoutConstraint activateConstraints:@[
                    [label.centerXAnchor constraintEqualToAnchor:panel.contentView.centerXAnchor],
                    [label.centerYAnchor constraintEqualToAnchor:panel.contentView.centerYAnchor],
                    [label.widthAnchor constraintLessThanOrEqualToAnchor:panel.contentView.widthAnchor multiplier:0.9],
                ]];
            }
        }
    });
}

void mmprivacy_countdown(void *ptr, int seconds) {
    onMain(^{
        @autoreleasepool {
            PrivacyScreen *screen = (__bridge PrivacyScreen *)ptr;
            screen.countdown = seconds;
            for (PrivacyPanel *panel in screen.panels.allValues) {
                panel.message.stringValue = coverMessage(seconds);
            }
        }
    });
}

void mmprivacy_close(void *ptr) {
    onMain(^{
        @autoreleasepool {
            PrivacyScreen *screen = (__bridge_transfer PrivacyScreen *)ptr;
            [NSNotificationCenter.defaultCenter removeObserver:screen.displayObserver];
            UnregisterEventHotKey(screen.hotkey);
            RemoveEventHandler(screen.hotkeyHandler);
            for (PrivacyPanel *panel in screen.panels.allValues) {
                [panel orderOut:nil];
            }
        }
    });
}

#include <dlfcn.h>

const char *mmprivacy_lock(void) {
    static void (*lockNow)(void);
    static const char *failure;
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        void *login = dlopen("/System/Library/PrivateFrameworks/login.framework/login", RTLD_LOCAL | RTLD_LAZY);
        if (!login) {
            failure = "无法加载 macOS 登录组件";
            return;
        }
        lockNow = (void (*)(void))dlsym(login, "SACLockScreenImmediate");
        if (!lockNow) {
            failure = "macOS 登录组件缺少锁屏接口";
        }
    });
    if (failure) return failure;
    lockNow();
    return NULL;
}

int mmprivacy_locked(void) {
    @autoreleasepool {
        NSDictionary *info = CFBridgingRelease(CGSessionCopyCurrentDictionary());
        if (info == nil) return -1;
        return [info[@"CGSSessionScreenIsLocked"] boolValue] ? 1 : 0;
    }
}

void mmprivacy_pump_main(void) {
    @autoreleasepool {
        CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.01, true);
    }
}
