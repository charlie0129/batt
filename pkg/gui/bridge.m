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
- (void)setInterval:(NSTimeInterval)intervalSeconds;
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
    return [NSColor colorWithCalibratedWhite:0.92 alpha:1.0];
}

static bool batt_lowPowerModeEnabled(void) {
    if (@available(macOS 12.0, *)) {
        return [[NSProcessInfo processInfo] isLowPowerModeEnabled];
    }
    return false;
}

static NSColor *batt_batteryFillColor(int percent, bool charging, bool paused, bool thermalPaused) {
    if (batt_lowPowerModeEnabled()) return batt_yellowColor();
    if (charging || paused || thermalPaused || percent >= 20) return batt_greenColor();
    return batt_redColor();
}

static NSColor *batt_percentageFillColor(int percent, bool charging, bool paused, bool thermalPaused) {
    if (batt_lowPowerModeEnabled()) return batt_yellowColor();
    if (charging || paused || thermalPaused || percent >= 20) return batt_greenColor();
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

static void batt_drawThermalPauseInRect(NSRect rect, NSColor *fillColor, NSColor *strokeColor) {
    (void)fillColor;

    CGFloat thermometerWidth = MIN(rect.size.width, MAX(7.0, floor(rect.size.width * 0.72)));
    NSRect thermometerRect = NSMakeRect(rect.origin.x + floor((rect.size.width - thermometerWidth) / 2.0),
                                        rect.origin.y,
                                        thermometerWidth,
                                        rect.size.height);

    CGFloat stemWidth = MAX(2.4, floor(thermometerRect.size.width * 0.34));
    CGFloat stemX = thermometerRect.origin.x + floor((thermometerRect.size.width - stemWidth) / 2.0);
    NSRect stem = NSMakeRect(stemX,
                             thermometerRect.origin.y + thermometerRect.size.height * 0.28,
                             stemWidth,
                             thermometerRect.size.height * 0.64);
    CGFloat bulbSize = stemWidth * 2.25;
    NSRect bulb = NSMakeRect(thermometerRect.origin.x + floor((thermometerRect.size.width - bulbSize) / 2.0),
                             thermometerRect.origin.y,
                             bulbSize,
                             bulbSize);
    NSBezierPath *stemPath = [NSBezierPath bezierPathWithRoundedRect:stem xRadius:stemWidth / 2.0 yRadius:stemWidth / 2.0];
    NSBezierPath *bulbPath = [NSBezierPath bezierPathWithOvalInRect:bulb];
    NSColor *thermometerColor = [NSColor colorWithCalibratedRed:1.0 green:0.18 blue:0.10 alpha:1.0];
    NSColor *outlineColor = strokeColor ? strokeColor : [NSColor whiteColor];

    [outlineColor setStroke];
    [stemPath setLineWidth:1.6];
    [bulbPath setLineWidth:1.6];
    [stemPath stroke];
    [bulbPath stroke];

    [thermometerColor setFill];
    [stemPath fill];
    [bulbPath fill];

    NSBezierPath *ticks = [NSBezierPath bezierPath];
    CGFloat tickX1 = MIN(NSMaxX(stem) + 1.0, NSMaxX(thermometerRect) - 3.0);
    CGFloat tickX2 = NSMaxX(thermometerRect) - 0.6;
    for (int i = 0; i < 3; i++) {
        CGFloat y = stem.origin.y + stem.size.height * (0.25 + i * 0.24);
        [ticks moveToPoint:NSMakePoint(tickX1, y)];
        [ticks lineToPoint:NSMakePoint(tickX2, y)];
    }
    [ticks setLineWidth:1.0];
    [ticks setLineCapStyle:NSRoundLineCapStyle];
    [[NSColor whiteColor] setStroke];
    [ticks stroke];
}

static void batt_drawFittingCenteredText(NSString *text, NSRect rect, NSColor *color, CGFloat maxSize, CGFloat minSize, NSFontWeight weight) {
    NSMutableParagraphStyle *paragraph = [[NSMutableParagraphStyle alloc] init];
    paragraph.alignment = NSTextAlignmentCenter;

    CGFloat size = maxSize;
    NSDictionary *attrs = nil;
    while (size >= minSize) {
        NSFont *font = [NSFont monospacedDigitSystemFontOfSize:size weight:weight];
        attrs = @{
            NSFontAttributeName: font,
            NSForegroundColorAttributeName: color,
            NSParagraphStyleAttributeName: paragraph
        };
        NSSize textSize = [text sizeWithAttributes:attrs];
        if (textSize.width <= rect.size.width && textSize.height <= rect.size.height + 2.0) {
            break;
        }
        size -= 0.5;
    }

    NSSize textSize = [text sizeWithAttributes:attrs];
    NSRect textRect = NSMakeRect(rect.origin.x,
                                 rect.origin.y + floor((rect.size.height - textSize.height) / 2.0),
                                 rect.size.width,
                                 textSize.height);

    [NSGraphicsContext saveGraphicsState];
    NSBezierPath *clip = [NSBezierPath bezierPathWithRect:rect];
    [clip addClip];
    [text drawInRect:textRect withAttributes:attrs];
    [NSGraphicsContext restoreGraphicsState];

#if !__has_feature(objc_arc)
    [paragraph release];
#endif
}

static NSRect batt_centeredImageRect(NSImage *source, NSRect bounds) {
    NSSize sourceSize = source.size;
    if (sourceSize.width <= 0.0 || sourceSize.height <= 0.0) {
        return bounds;
    }

    CGFloat scale = MIN(bounds.size.width / sourceSize.width, bounds.size.height / sourceSize.height);
    CGFloat width = floor(sourceSize.width * scale);
    CGFloat height = floor(sourceSize.height * scale);
    return NSMakeRect(bounds.origin.x + floor((bounds.size.width - width) / 2.0),
                      bounds.origin.y + floor((bounds.size.height - height) / 2.0),
                      width,
                      height);
}

static void batt_drawTintedImage(NSImage *source, NSRect bounds, NSColor *color) {
    NSRect drawRect = batt_centeredImageRect(source, bounds);
    [source drawInRect:drawRect
              fromRect:NSZeroRect
             operation:NSCompositingOperationSourceOver
              fraction:1.0
        respectFlipped:NO
                 hints:nil];
    [color setFill];
    NSRectFillUsingOperation(drawRect, NSCompositingOperationSourceIn);
}

static NSImage *batt_newBatteryOutlineIcon(int percent, bool charging, bool paused, bool thermalPaused) {
    percent = batt_clampPercent(percent);
    NSSize size = NSMakeSize(33.0, 18.0);
    NSImage *image = [[NSImage alloc] initWithSize:size];
    [image setTemplate:NO];
    [image setAccessibilityDescription:@"batt battery icon"];

    [image lockFocus];
    NSRect body = NSMakeRect(1.0, 2.7, 27.0, 12.6);
    NSRect terminal = NSMakeRect(29.0, 5.4, 2.8, 7.2);
    NSBezierPath *bodyPath = [NSBezierPath bezierPathWithRoundedRect:body xRadius:3.1 yRadius:3.1];
    NSBezierPath *terminalPath = [NSBezierPath bezierPathWithRoundedRect:terminal xRadius:1.3 yRadius:1.3];

    [batt_outlineColor() setStroke];
    [bodyPath setLineWidth:1.2];
    [bodyPath stroke];
    [batt_outlineColor() setFill];
    [terminalPath fill];

    CGFloat fillWidth = floor((body.size.width - 4.8) * percent / 100.0);
    if (percent > 0 && fillWidth < 1.8) fillWidth = 1.8;
    NSRect fillRect = NSMakeRect(body.origin.x + 2.4, body.origin.y + 2.4, fillWidth, body.size.height - 4.8);
    NSBezierPath *fillPath = [NSBezierPath bezierPathWithRoundedRect:fillRect xRadius:0.9 yRadius:0.9];
    [batt_batteryFillColor(percent, charging, paused, thermalPaused) setFill];
    [fillPath fill];

    if (thermalPaused) {
        batt_drawThermalPauseInRect(NSMakeRect(5.2, 3.5, 20.0, 11.0), [NSColor whiteColor], [NSColor colorWithCalibratedWhite:0.20 alpha:0.65]);
    } else if (paused) {
        batt_drawPauseInRect(NSMakeRect(10.7, 4.8, 8.6, 8.2), [NSColor whiteColor], [NSColor colorWithCalibratedWhite:0.20 alpha:0.65]);
    } else if (charging) {
        batt_drawBoltInRect(NSMakeRect(10.5, 2.5, 9.8, 12.8), [NSColor whiteColor], [NSColor colorWithCalibratedWhite:0.20 alpha:0.65]);
    }

    [image unlockFocus];
    return image;
}

static NSImage *batt_newFixedPercentageIcon(int percent, bool charging, bool paused, bool thermalPaused) {
    (void)charging;
    (void)paused;
    (void)thermalPaused;

    percent = batt_clampPercent(percent);
    NSSize size = NSMakeSize(34.0, 16.0);
    NSImage *image = [[NSImage alloc] initWithSize:size];
    [image setTemplate:NO];
    [image setAccessibilityDescription:@"batt fixed percentage icon"];

    [image lockFocus];
    NSRect symbolRect = NSMakeRect(0.0, 0.0, 32.0, 16.0);
    bool drewLegacySymbol = false;
    if (@available(macOS 11.0, *)) {
        NSImage *symbol = [NSImage imageWithSystemSymbolName:@"minus.plus.batteryblock" accessibilityDescription:@"batt icon"];
        if (symbol) {
            batt_drawTintedImage(symbol, symbolRect, [NSColor whiteColor]);
            drewLegacySymbol = true;
        }
    }

    if (!drewLegacySymbol) {
        NSRect body = NSMakeRect(1.0, 2.7, 26.5, 10.6);
        NSRect terminal = NSMakeRect(28.6, 5.3, 2.8, 5.4);
        NSBezierPath *bodyPath = [NSBezierPath bezierPathWithRoundedRect:body xRadius:2.7 yRadius:2.7];
        NSBezierPath *terminalPath = [NSBezierPath bezierPathWithRoundedRect:terminal xRadius:1.2 yRadius:1.2];
        [[NSColor whiteColor] setStroke];
        [bodyPath setLineWidth:1.45];
        [bodyPath stroke];
        [[NSColor whiteColor] setFill];
        [terminalPath fill];
    }

    NSString *text = [NSString stringWithFormat:@"%d", percent];
    NSRect legacyGlyphRect = NSMakeRect(5.7, 4.0, 18.0, 8.0);
    NSRect textRect = NSMakeRect(5.3, 3.9, 18.8, 8.2);

    [NSGraphicsContext saveGraphicsState];
    NSBezierPath *clearPath = [NSBezierPath bezierPathWithRoundedRect:legacyGlyphRect xRadius:1.8 yRadius:1.8];
    [[NSGraphicsContext currentContext] setCompositingOperation:NSCompositingOperationClear];
    [clearPath fill];
    [NSGraphicsContext restoreGraphicsState];
    batt_drawFittingCenteredText(text, textRect, [NSColor whiteColor], 8.6, 5.8, NSFontWeightBold);

    [image unlockFocus];
    return image;
}

static NSImage *batt_newPercentageIcon(int percent, bool charging, bool paused, bool thermalPaused) {
    percent = batt_clampPercent(percent);
    NSSize size = NSMakeSize(40.0, 20.0);
    NSImage *image = [[NSImage alloc] initWithSize:size];
    [image setTemplate:NO];
    [image setAccessibilityDescription:@"batt battery percentage icon"];

    [image lockFocus];
    NSRect body = NSMakeRect(0.5, 1.7, 35.0, 16.6);
    NSRect terminal = NSMakeRect(36.2, 5.8, 2.8, 8.4);
    NSBezierPath *bodyPath = [NSBezierPath bezierPathWithRoundedRect:body xRadius:4.8 yRadius:4.8];
    NSBezierPath *terminalPath = [NSBezierPath bezierPathWithRoundedRect:terminal xRadius:1.3 yRadius:1.3];

    NSColor *fillColor = batt_percentageFillColor(percent, charging, paused, thermalPaused);
    NSColor *trackColor = percent < 20 ? batt_trackGrayColor() : [NSColor whiteColor];
    if (percent >= 95) {
        trackColor = fillColor;
    }

    [NSGraphicsContext saveGraphicsState];
    [bodyPath addClip];
    [trackColor setFill];
    NSRectFill(body);

    CGFloat fillWidth = floor(body.size.width * percent / 100.0);
    if (percent > 0 && fillWidth < 2.4) fillWidth = 2.4;
    [fillColor setFill];
    NSRectFill(NSMakeRect(body.origin.x, body.origin.y, fillWidth, body.size.height));
    [NSGraphicsContext restoreGraphicsState];

    [trackColor setFill];
    [terminalPath fill];

    NSColor *textColor = (percent >= 95 || percent < 20) ? [NSColor whiteColor] : batt_darkTextColor();
    NSString *text = [NSString stringWithFormat:@"%d", percent];
    NSRect textRect = NSMakeRect(body.origin.x, body.origin.y, body.size.width, body.size.height);
    batt_drawFittingCenteredText(text, textRect, textColor, 13.8, 10.0, NSFontWeightBlack);

    if (thermalPaused) {
        batt_drawThermalPauseInRect(NSMakeRect(body.origin.x + body.size.width - 10.8, body.origin.y + 3.0, 9.0, 11.0),
                                    textColor,
                                    nil);
    } else if (paused) {
        batt_drawPauseInRect(NSMakeRect(body.origin.x + body.size.width - 9.0, body.origin.y + 4.4, 6.6, 8.2),
                             textColor,
                             nil);
    } else if (charging) {
        batt_drawBoltInRect(NSMakeRect(body.origin.x + body.size.width - 9.6, body.origin.y + 3.6, 6.8, 10.0),
                            textColor,
                            nil);
    }

    [image unlockFocus];
    return image;
}

void batt_setMenubarBatteryIcon(uintptr_t statusItemPtr, const char* style, int percent, bool charging, bool paused, bool thermalPaused) {
    NSStatusItem *item = (NSStatusItem *)statusItemPtr;
    if (!item) return;
    NSStatusBarButton *button = [item button];
    if (!button) return;

    NSString *styleString = style ? [NSString stringWithUTF8String:style] : @"";
    NSImage *image = nil;
    if ([styleString isEqualToString:@"fixed-percentage"]) {
        image = batt_newFixedPercentageIcon(percent, charging, paused, thermalPaused);
    } else if ([styleString isEqualToString:@"battery"]) {
        image = batt_newBatteryOutlineIcon(percent, charging, paused, thermalPaused);
    } else {
        image = batt_newPercentageIcon(percent, charging, paused, thermalPaused);
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
- (void)setInterval:(NSTimeInterval)intervalSeconds {
    if (intervalSeconds <= 0.0 || isnan(intervalSeconds) || isinf(intervalSeconds)) {
        intervalSeconds = 60.0;
    }
    if (self.timer) {
        [self.timer invalidate];
        self.timer = nil;
    }
    self.timer = [NSTimer timerWithTimeInterval:intervalSeconds
                                         target:self
                                       selector:@selector(timerTick:)
                                       userInfo:nil
                                        repeats:YES];
    [[NSRunLoop mainRunLoop] addTimer:self.timer forMode:NSRunLoopCommonModes];
}
- (void)timerTick:(NSTimer *)timer {
    battTrayIconTimerFired(_handle);
}
@end

void *batt_attachTrayIconTimer(uintptr_t handle, double intervalSeconds) {
    BattTrayIconTimerTarget *target = [[BattTrayIconTimerTarget alloc] initWithHandle:handle];
    [target setInterval:intervalSeconds];
    return (void *)CFBridgingRetain(target);
}

void batt_setTrayIconTimerInterval(void *timerPtr, double intervalSeconds) {
    if (timerPtr == NULL) return;
    BattTrayIconTimerTarget *target = (BattTrayIconTimerTarget *)timerPtr;
    [target setInterval:intervalSeconds];
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
