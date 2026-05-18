#import <Cocoa/Cocoa.h>
#import <ServiceManagement/ServiceManagement.h>
#import <CoreFoundation/CoreFoundation.h>
#import <objc/runtime.h>
#include <stdint.h>
#include <stdbool.h>
#include <math.h>
// #import <UserNotifications/UserNotifications.h>

// The time interval in seconds for the menu update timer.
static const NSTimeInterval kMenuUpdateTimerInterval = 1.0;

// Callbacks exported from Go
extern void battMenuWillOpen(uintptr_t handle);
extern void battMenuDidClose(uintptr_t handle);
extern void battMenuTimerFired(uintptr_t handle);
extern void battTrayIconTimerFired(uintptr_t handle);
extern void battTemperatureThresholdChanged(uintptr_t handle, int value);

@interface BattMenuObserver : NSObject
@property(nonatomic, assign) uintptr_t handle;
@property(nonatomic, strong) NSTimer *timer;
- (instancetype)initWithHandle:(uintptr_t)handle;
- (void)menuWillOpen:(NSNotification *)note;
- (void)menuDidClose:(NSNotification *)note;
- (void)timerTick:(NSTimer *)timer;
@end

@interface BattTemperatureSliderTarget : NSObject
@property(nonatomic, assign) uintptr_t handle;
@property(nonatomic, strong) NSSlider *slider;
@property(nonatomic, strong) NSTextField *valueLabel;
- (instancetype)initWithHandle:(uintptr_t)handle;
- (void)sliderChanged:(NSSlider *)sender;
- (void)setValue:(int)value;
- (void)setEnabled:(BOOL)enabled;
@end

@interface BattTrayIconTimerTarget : NSObject
@property(nonatomic, assign) uintptr_t handle;
@property(nonatomic, strong) NSTimer *timer;
- (instancetype)initWithHandle:(uintptr_t)handle;
- (void)timerTick:(NSTimer *)timer;
@end

@implementation BattTemperatureSliderTarget
- (instancetype)initWithHandle:(uintptr_t)handle {
    if ((self = [super init])) {
        _handle = handle;
    }
    return self;
}
- (void)sliderChanged:(NSSlider *)sender {
    int value = (int)lround(sender.doubleValue);
    [self setValue:value];
    if (self.handle != 0) {
        battTemperatureThresholdChanged(self.handle, value);
    }
}
- (void)setValue:(int)value {
    self.slider.integerValue = value;
    self.valueLabel.stringValue = [NSString stringWithFormat:@"%d°C", value];
}
- (void)setEnabled:(BOOL)enabled {
    self.slider.enabled = enabled;
    self.valueLabel.enabled = enabled;
}
@end

static const void *kBattTemperatureSliderTargetKey = &kBattTemperatureSliderTargetKey;

static NSTextField *batt_label(NSString *text, NSRect frame, NSFont *font, NSColor *color) {
    NSTextField *label = [[NSTextField alloc] initWithFrame:frame];
    label.stringValue = text;
    label.font = font;
    label.textColor = color;
    label.bezeled = NO;
    label.drawsBackground = NO;
    label.editable = NO;
    label.selectable = NO;
    return label;
}

void *batt_newTemperatureSliderMenuItem(uintptr_t handle, int minValue, int maxValue, int value) {
    NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:@"" action:nil keyEquivalent:@""];
    NSView *view = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, 280, 66)];

    BattTemperatureSliderTarget *target = [[BattTemperatureSliderTarget alloc] initWithHandle:handle];

    NSTextField *title = batt_label(@"Temperature Protection", NSMakeRect(14, 40, 180, 18),
                                    [NSFont systemFontOfSize:13 weight:NSFontWeightRegular],
                                    [NSColor labelColor]);
    [view addSubview:title];

    NSTextField *valueLabel = batt_label(@"", NSMakeRect(218, 40, 48, 18),
                                         [NSFont monospacedDigitSystemFontOfSize:13 weight:NSFontWeightMedium],
                                         [NSColor secondaryLabelColor]);
    valueLabel.alignment = NSTextAlignmentRight;
    [view addSubview:valueLabel];

    NSSlider *slider = [[NSSlider alloc] initWithFrame:NSMakeRect(12, 10, 254, 24)];
    slider.minValue = minValue;
    slider.maxValue = maxValue;
    slider.numberOfTickMarks = 6;
    slider.allowsTickMarkValuesOnly = NO;
    slider.continuous = NO;
    slider.target = target;
    slider.action = @selector(sliderChanged:);
    [view addSubview:slider];

    target.slider = slider;
    target.valueLabel = valueLabel;
    [target setValue:value];

    objc_setAssociatedObject(item, kBattTemperatureSliderTargetKey, target, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
    item.view = view;

    return (void *)CFBridgingRetain(item);
}

static BattTemperatureSliderTarget *batt_temperatureSliderTarget(void *itemPtr) {
    if (itemPtr == NULL) return nil;
    NSMenuItem *item = (NSMenuItem *)itemPtr;
    return objc_getAssociatedObject(item, kBattTemperatureSliderTargetKey);
}

void batt_setTemperatureSliderHandle(void *itemPtr, uintptr_t handle) {
    BattTemperatureSliderTarget *target = batt_temperatureSliderTarget(itemPtr);
    if (target) {
        target.handle = handle;
    }
}

void batt_setTemperatureSliderValue(void *itemPtr, int value) {
    BattTemperatureSliderTarget *target = batt_temperatureSliderTarget(itemPtr);
    if (target) {
        [target setValue:value];
    }
}

void batt_setTemperatureSliderEnabled(void *itemPtr, bool enabled) {
    BattTemperatureSliderTarget *target = batt_temperatureSliderTarget(itemPtr);
    if (target) {
        [target setEnabled:enabled];
    }
}

void batt_releaseObject(void *objPtr) {
    if (objPtr == NULL) return;
    CFRelease(objPtr);
}

static int batt_clampPercent(int percent) {
    if (percent < 0) return 0;
    if (percent > 100) return 100;
    return percent;
}

static NSColor *batt_greenColor(void) {
    return [NSColor colorWithCalibratedRed:0.18 green:0.78 blue:0.32 alpha:1.0];
}

static NSColor *batt_yellowColor(void) {
    return [NSColor colorWithCalibratedRed:1.0 green:0.78 blue:0.06 alpha:1.0];
}

static NSColor *batt_redColor(void) {
    return [NSColor colorWithCalibratedRed:1.0 green:0.15 blue:0.12 alpha:1.0];
}

static NSColor *batt_darkTextColor(void) {
    return [NSColor colorWithCalibratedWhite:0.16 alpha:1.0];
}

static NSColor *batt_trackGrayColor(void) {
    return [NSColor colorWithCalibratedWhite:0.62 alpha:1.0];
}

static NSColor *batt_outlineColor(void) {
    return [NSColor colorWithCalibratedWhite:0.52 alpha:1.0];
}

static NSColor *batt_batteryFillColor(int percent, bool charging) {
    if (charging || percent >= 50) return batt_greenColor();
    if (percent >= 20) return batt_yellowColor();
    return batt_redColor();
}

static NSColor *batt_percentageFillColor(int percent, bool charging) {
    if (charging || percent >= 80) return batt_greenColor();
    if (percent >= 20) return batt_yellowColor();
    return batt_redColor();
}

static void batt_drawBoltInRect(NSRect rect, NSColor *fillColor, NSColor *strokeColor) {
    NSBezierPath *bolt = [NSBezierPath bezierPath];
    CGFloat x = rect.origin.x;
    CGFloat y = rect.origin.y;
    CGFloat w = rect.size.width;
    CGFloat h = rect.size.height;

    [bolt moveToPoint:NSMakePoint(x + 0.58 * w, y + h)];
    [bolt lineToPoint:NSMakePoint(x + 0.12 * w, y + 0.43 * h)];
    [bolt lineToPoint:NSMakePoint(x + 0.44 * w, y + 0.43 * h)];
    [bolt lineToPoint:NSMakePoint(x + 0.28 * w, y)];
    [bolt lineToPoint:NSMakePoint(x + 0.88 * w, y + 0.58 * h)];
    [bolt lineToPoint:NSMakePoint(x + 0.56 * w, y + 0.58 * h)];
    [bolt closePath];
    [bolt setLineJoinStyle:NSRoundLineJoinStyle];

    if (strokeColor) {
        [strokeColor setStroke];
        [bolt setLineWidth:1.8];
        [bolt stroke];
    }
    [fillColor setFill];
    [bolt fill];
}

static void batt_drawPauseInRect(NSRect rect, NSColor *fillColor, NSColor *strokeColor) {
    CGFloat barWidth = rect.size.width * 0.28;
    CGFloat gap = rect.size.width * 0.18;
    CGFloat totalWidth = barWidth * 2.0 + gap;
    CGFloat x = rect.origin.x + floor((rect.size.width - totalWidth) / 2.0);
    CGFloat radius = barWidth / 2.0;
    NSRect left = NSMakeRect(x, rect.origin.y, barWidth, rect.size.height);
    NSRect right = NSMakeRect(x + barWidth + gap, rect.origin.y, barWidth, rect.size.height);
    NSBezierPath *leftPath = [NSBezierPath bezierPathWithRoundedRect:left xRadius:radius yRadius:radius];
    NSBezierPath *rightPath = [NSBezierPath bezierPathWithRoundedRect:right xRadius:radius yRadius:radius];

    if (strokeColor) {
        [strokeColor setStroke];
        [leftPath setLineWidth:1.4];
        [rightPath setLineWidth:1.4];
        [leftPath stroke];
        [rightPath stroke];
    }

    [fillColor setFill];
    [leftPath fill];
    [rightPath fill];
}

static void batt_drawCenteredText(NSString *text, NSRect rect, NSColor *color, CGFloat size, NSFontWeight weight) {
    NSFont *font = [NSFont monospacedDigitSystemFontOfSize:size weight:weight];
    NSMutableParagraphStyle *paragraph = [[NSMutableParagraphStyle alloc] init];
    paragraph.alignment = NSTextAlignmentCenter;
    NSDictionary *attrs = @{
        NSFontAttributeName: font,
        NSForegroundColorAttributeName: color,
        NSParagraphStyleAttributeName: paragraph
    };

    NSSize textSize = [text sizeWithAttributes:attrs];
    NSRect textRect = NSMakeRect(rect.origin.x,
                                 rect.origin.y + floor((rect.size.height - textSize.height) / 2.0) - 0.5,
                                 rect.size.width,
                                 textSize.height);
    [text drawInRect:textRect withAttributes:attrs];

#if !__has_feature(objc_arc)
    [paragraph release];
#endif
}

static NSImage *batt_newBatteryOutlineIcon(int percent, bool charging, bool paused) {
    percent = batt_clampPercent(percent);
    NSSize size = NSMakeSize(40.0, 20.0);
    NSImage *image = [[NSImage alloc] initWithSize:size];
    [image setTemplate:NO];
    [image setAccessibilityDescription:@"batt battery icon"];

    [image lockFocus];
    NSRect body = NSMakeRect(1.5, 3.0, 34.0, 14.0);
    NSRect terminal = NSMakeRect(36.0, 7.0, 3.5, 6.0);
    NSBezierPath *bodyPath = [NSBezierPath bezierPathWithRoundedRect:body xRadius:4.0 yRadius:4.0];
    NSBezierPath *terminalPath = [NSBezierPath bezierPathWithRoundedRect:terminal xRadius:1.7 yRadius:1.7];

    [batt_outlineColor() setStroke];
    [bodyPath setLineWidth:2.2];
    [bodyPath stroke];
    [batt_outlineColor() setFill];
    [terminalPath fill];

    CGFloat fillWidth = floor((body.size.width - 7.0) * percent / 100.0);
    if (percent > 0 && fillWidth < 2.0) fillWidth = 2.0;
    NSRect fillRect = NSMakeRect(body.origin.x + 3.5, body.origin.y + 3.5, fillWidth, body.size.height - 7.0);
    NSBezierPath *fillPath = [NSBezierPath bezierPathWithRoundedRect:fillRect xRadius:1.5 yRadius:1.5];
    [batt_batteryFillColor(percent, charging) setFill];
    [fillPath fill];

    if (paused) {
        batt_drawPauseInRect(NSMakeRect(14.0, 4.0, 11.0, 12.0), [NSColor whiteColor], [NSColor colorWithCalibratedWhite:0.20 alpha:0.65]);
    } else if (charging) {
        batt_drawBoltInRect(NSMakeRect(13.5, 1.0, 13.5, 18.0), [NSColor whiteColor], [NSColor colorWithCalibratedWhite:0.20 alpha:0.65]);
    }

    [image unlockFocus];
    return image;
}

static NSImage *batt_newPercentageIcon(int percent, bool charging, bool paused) {
    percent = batt_clampPercent(percent);
    NSSize size = NSMakeSize(55.0, 20.0);
    NSImage *image = [[NSImage alloc] initWithSize:size];
    [image setTemplate:NO];
    [image setAccessibilityDescription:@"batt battery percentage icon"];

    [image lockFocus];
    NSRect body = NSMakeRect(0.5, 1.0, 50.0, 18.0);
    NSRect terminal = NSMakeRect(51.0, 7.0, 3.5, 6.0);
    NSBezierPath *bodyPath = [NSBezierPath bezierPathWithRoundedRect:body xRadius:6.5 yRadius:6.5];
    NSBezierPath *terminalPath = [NSBezierPath bezierPathWithRoundedRect:terminal xRadius:1.8 yRadius:1.8];

    NSColor *fillColor = batt_percentageFillColor(percent, charging);
    NSColor *trackColor = percent < 20 ? batt_trackGrayColor() : [NSColor whiteColor];
    if (percent >= 95) {
        trackColor = fillColor;
    }

    [NSGraphicsContext saveGraphicsState];
    [bodyPath addClip];
    [trackColor setFill];
    NSRectFill(body);

    CGFloat fillWidth = floor(body.size.width * percent / 100.0);
    if (percent > 0 && fillWidth < 3.0) fillWidth = 3.0;
    [fillColor setFill];
    NSRectFill(NSMakeRect(body.origin.x, body.origin.y, fillWidth, body.size.height));
    [NSGraphicsContext restoreGraphicsState];

    [trackColor setFill];
    [terminalPath fill];

    NSColor *textColor = (percent >= 95 || percent < 20) ? [NSColor whiteColor] : batt_darkTextColor();
    NSString *text = [NSString stringWithFormat:@"%d", percent];
    NSRect textRect = (charging || paused) ? NSMakeRect(body.origin.x + 1.5, body.origin.y, body.size.width - 13.0, body.size.height)
                                           : NSMakeRect(body.origin.x, body.origin.y, body.size.width, body.size.height);
    batt_drawCenteredText(text, textRect, textColor, 16.0, NSFontWeightMedium);

    if (paused) {
        batt_drawPauseInRect(NSMakeRect(body.origin.x + body.size.width - 11.5, body.origin.y + 5.0, 7.5, 8.0),
                             textColor,
                             nil);
    } else if (charging) {
        batt_drawBoltInRect(NSMakeRect(body.origin.x + body.size.width - 12.0, body.origin.y + 4.0, 8.0, 10.5),
                            textColor,
                            nil);
    }

    [image unlockFocus];
    return image;
}

void batt_setMenubarBatteryIcon(uintptr_t statusItemPtr, const char* style, int percent, bool charging, bool paused) {
    NSStatusItem *item = (NSStatusItem *)statusItemPtr;
    if (!item) return;
    NSStatusBarButton *button = [item button];
    if (!button) return;

    NSString *styleString = style ? [NSString stringWithUTF8String:style] : @"";
    NSImage *image = nil;
    if ([styleString isEqualToString:@"battery"]) {
        image = batt_newBatteryOutlineIcon(percent, charging, paused);
    } else {
        image = batt_newPercentageIcon(percent, charging, paused);
    }

    [item setLength:NSVariableStatusItemLength];
    [button setImage:image];
    [button setImagePosition:NSImageOnly];
    [button setImageScaling:NSImageScaleProportionallyDown];

#if !__has_feature(objc_arc)
    [image release];
#endif
}

@implementation BattTrayIconTimerTarget
- (instancetype)initWithHandle:(uintptr_t)handle {
    if ((self = [super init])) {
        _handle = handle;
    }
    return self;
}
- (void)timerTick:(NSTimer *)timer {
    battTrayIconTimerFired(_handle);
}
@end

void *batt_attachTrayIconTimer(uintptr_t handle, double intervalSeconds) {
    BattTrayIconTimerTarget *target = [[BattTrayIconTimerTarget alloc] initWithHandle:handle];
    target.timer = [NSTimer timerWithTimeInterval:intervalSeconds
                                           target:target
                                         selector:@selector(timerTick:)
                                         userInfo:nil
                                          repeats:YES];
    [[NSRunLoop mainRunLoop] addTimer:target.timer forMode:NSRunLoopCommonModes];
    return (void *)CFBridgingRetain(target);
}

void batt_releaseTrayIconTimer(void *timerPtr) {
    if (timerPtr == NULL) return;
    BattTrayIconTimerTarget *target = (BattTrayIconTimerTarget *)timerPtr;
    if (target.timer) {
        [target.timer invalidate];
        target.timer = nil;
    }
    CFRelease(timerPtr);
}

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
