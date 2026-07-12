#import <Cocoa/Cocoa.h>
#import <ServiceManagement/ServiceManagement.h>

#include "native.h"

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"

@interface BattNotificationDispatcher : NSObject
+ (void)deliver:(NSArray<NSString *> *)parts;
@end

@implementation BattNotificationDispatcher
+ (void)deliver:(NSArray<NSString *> *)parts {
    NSUserNotification *notification = [[[NSUserNotification alloc] init] autorelease];
    notification.title = [parts objectAtIndex:0];
    notification.informativeText = [parts objectAtIndex:1];
    notification.soundName = NSUserNotificationDefaultSoundName;
    [[NSUserNotificationCenter defaultUserNotificationCenter]
        deliverNotification:notification];
}
@end

void batt_show_notification(const char *title, const char *body) {
    @autoreleasepool {
        NSString *nativeTitle = title == NULL ? @"" : [NSString stringWithUTF8String:title];
        NSString *nativeBody = body == NULL ? @"" : [NSString stringWithUTF8String:body];
        NSArray *parts = [NSArray arrayWithObjects:nativeTitle, nativeBody, nil];
        [BattNotificationDispatcher performSelectorOnMainThread:@selector(deliver:)
                                                     withObject:parts
                                                  waitUntilDone:NO];
    }
}

bool batt_register_login_item(void) {
    if (@available(macOS 13.0, *)) {
        NSError *error = nil;
        BOOL success = [[SMAppService mainAppService] registerAndReturnError:&error];
        if (!success && error != nil) {
            NSLog(@"Failed to register login item: %@", error);
        }
        return success;
    }
    NSLog(@"SMAppService not available on this macOS version");
    return false;
}

bool batt_unregister_login_item(void) {
    if (@available(macOS 13.0, *)) {
        NSError *error = nil;
        BOOL success = [[SMAppService mainAppService] unregisterAndReturnError:&error];
        if (!success && error != nil) {
            NSLog(@"Failed to unregister login item: %@", error);
        }
        return success;
    }
    NSLog(@"SMAppService not available on this macOS version");
    return false;
}

bool batt_is_login_item_registered(void) {
    if (@available(macOS 13.0, *)) {
        return [SMAppService mainAppService].status == SMAppServiceStatusEnabled;
    }
    return false;
}

#pragma clang diagnostic pop
