#import <Cocoa/Cocoa.h>
#import <ServiceManagement/ServiceManagement.h>
#import <CoreFoundation/CoreFoundation.h>
#include <stdint.h>
// #import <UserNotifications/UserNotifications.h>

// The time interval in seconds for the menu update timer.
static const NSTimeInterval kMenuUpdateTimerInterval = 1.0;

// Callbacks exported from Go
extern void battMenuWillOpen(uintptr_t handle);
extern void battMenuDidClose(uintptr_t handle);
extern void battMenuTimerFired(uintptr_t handle);

@interface BattMenuObserver : NSObject
@property(nonatomic, assign) uintptr_t handle;
@property(nonatomic, strong) NSTimer *timer;
- (instancetype)initWithHandle:(uintptr_t)handle;
- (void)menuWillOpen:(NSNotification *)note;
- (void)menuDidClose:(NSNotification *)note;
- (void)timerTick:(NSTimer *)timer;
@end

@implementation BattMenuObserver
- (instancetype)initWithHandle:(uintptr_t)handle {
    if ((self = [super init])) {
        _handle = handle;
    }
    return self;
}
- (void)menuWillOpen:(NSNotification *)note {
    battMenuWillOpen(_handle);
    // Start a selector-based timer and add it to common modes so it fires during menu tracking.
    self.timer = [NSTimer timerWithTimeInterval:kMenuUpdateTimerInterval
                                         target:self
                                       selector:@selector(timerTick:)
                                       userInfo:nil
                                        repeats:YES];
    [[NSRunLoop mainRunLoop] addTimer:self.timer forMode:NSRunLoopCommonModes];
}
- (void)menuDidClose:(NSNotification *)note {
    if (self.timer) {
        [self.timer invalidate];
        self.timer = nil;
    }
    battMenuDidClose(_handle);
}
- (void)timerTick:(NSTimer *)timer {
    battMenuTimerFired(_handle);
}
@end

void *batt_attachMenuObserver(uintptr_t menuPtr, uintptr_t handle) {
    NSMenu *menu = (NSMenu *)menuPtr;
    BattMenuObserver *obs = [[BattMenuObserver alloc] initWithHandle:handle];
    NSNotificationCenter *center = [NSNotificationCenter defaultCenter];
    [center addObserver:obs selector:@selector(menuWillOpen:) name:NSMenuDidBeginTrackingNotification object:menu];
    [center addObserver:obs selector:@selector(menuDidClose:) name:NSMenuDidEndTrackingNotification object:menu];
    return (void *)CFBridgingRetain(obs);
}

void batt_releaseMenuObserver(void *obsPtr) {
    if (obsPtr == NULL) return;
    BattMenuObserver *obs = (BattMenuObserver *)obsPtr;
    if (obs.timer) {
        [obs.timer invalidate];
        obs.timer = nil;
    }
    [[NSNotificationCenter defaultCenter] removeObserver:obs];
    CFRelease(obsPtr);
}

void batt_showNotification(const char* title, const char* body) {
    @autoreleasepool {
        NSString *nsTitle = title ? [NSString stringWithUTF8String:title] : @"";
        NSString *nsBody = body ? [NSString stringWithUTF8String:body] : @"";
        
        NSUserNotification *notification = [[NSUserNotification alloc] init];
        notification.title = nsTitle;
        notification.informativeText = nsBody;
        notification.soundName = NSUserNotificationDefaultSoundName;
        [[NSUserNotificationCenter defaultUserNotificationCenter] deliverNotification:notification];
    }
}

// need codesign app bundle
// void batt_showNotification(const char* title, const char* body) {
//     @autoreleasepool {
//         NSString *nsTitle = title ? [NSString stringWithUTF8String:title] : @"";
//         NSString *nsBody = body ? [NSString stringWithUTF8String:body] : @"";

//         UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
//         // Request authorization if needed (best-effort)
//         [center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound)
//                                 completionHandler:^(BOOL granted, NSError * _Nullable error) {
//             if (granted) {
//                 NSLog(@"Notification authorization granted");

//                 UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
//                 content.title = nsTitle;
//                 content.body = nsBody;
//                 content.sound = [UNNotificationSound defaultSound];

//                 UNTimeIntervalNotificationTrigger *trigger = [UNTimeIntervalNotificationTrigger triggerWithTimeInterval:0.1 repeats:NO];
//                 NSString *identifier = [[NSUUID UUID] UUIDString];
//                 UNNotificationRequest *request = [UNNotificationRequest requestWithIdentifier:identifier content:content trigger:trigger];
//                 [center addNotificationRequest:request withCompletionHandler:nil];
//             } else if (error) {
//                 NSLog(@"Notification authorization error: %@", error);
//             } else {
//                 NSLog(@"Notification authorization denied");
//             }
//         }];
//     }
// }

bool registerAppWithSMAppService(void) {
    if (@available(macOS 13.0, *)) {
        NSError *error = nil;
        SMAppService *service = [SMAppService mainAppService];
        BOOL success = [service registerAndReturnError:&error];
        if (!success && error) {
            NSLog(@"Failed to register login item: %@", error);
            return false;
        }
        return success;
    } else {
        NSLog(@"SMAppService not available on this macOS version");
        return false;
    }
}

bool unregisterAppWithSMAppService(void) {
    if (@available(macOS 13.0, *)) {
        NSError *error = nil;
        SMAppService *service = [SMAppService mainAppService];
        BOOL success = [service unregisterAndReturnError:&error];
        if (!success && error) {
            NSLog(@"Failed to unregister login item: %@", error);
            return false;
        }
        return success;
    } else {
        NSLog(@"SMAppService not available on this macOS version");
        return false;
    }
}

bool isRegisteredWithSMAppService(void) {
    if (@available(macOS 13.0, *)) {
        SMAppService *service = [SMAppService mainAppService];
        return [service status] == SMAppServiceStatusEnabled;
    }
    return false;
}