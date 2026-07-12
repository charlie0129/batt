#import "native_internal.h"

static const NSTimeInterval BattMenuUpdateInterval = 60.0;

static NSMenuItem *ActionItem(BattMenuController *controller,
                              NSString *title,
                              NSString *key,
                              BattMenuItem identifier) {
    NSMenuItem *item = [[[NSMenuItem alloc] initWithTitle:title
                                                  action:@selector(menuAction:)
                                           keyEquivalent:key] autorelease];
    item.target = controller;
    item.tag = identifier;
    [controller rememberItem:item as:identifier];
    return item;
}

static NSMenuItem *DisplayItem(BattMenuController *controller,
                               NSString *title,
                               BattMenuItem identifier,
                               BOOL enabled) {
    NSMenuItem *item = [[[NSMenuItem alloc] initWithTitle:title
                                                  action:@selector(noop:)
                                           keyEquivalent:@""] autorelease];
    item.target = controller;
    item.tag = identifier;
    item.enabled = enabled;
    [controller rememberItem:item as:identifier];
    return item;
}

static NSMenu *AddSubmenu(BattMenuController *controller,
                          NSMenu *parent,
                          NSString *title,
                          BattMenuItem identifier) {
    NSMenu *submenu = [[[NSMenu alloc] initWithTitle:title] autorelease];
    submenu.autoenablesItems = NO;
    NSMenuItem *item = [[[NSMenuItem alloc] initWithTitle:title
                                                  action:nil
                                           keyEquivalent:@""] autorelease];
    item.submenu = submenu;
    [controller rememberItem:item as:identifier];
    [parent addItem:item];
    return submenu;
}

@implementation BattMenuController

- (instancetype)initWithHandle:(uintptr_t)handle version:(NSString *)version {
    self = [super init];
    if (self) {
        _handle = handle;
        _items = [[NSMutableDictionary alloc] init];
        _menu = [[NSMenu alloc] initWithTitle:@"batt"];
        _menu.autoenablesItems = NO;
        _menu.delegate = self;
        _statusItem = [[[NSStatusBar systemStatusBar]
            statusItemWithLength:NSVariableStatusItemLength] retain];

        BattBuildMenu(self, version);
        BattApplyTooltips(self);
        _statusItem.menu = _menu;
        [self setStatusIconInstalled:NO capable:NO needsUpgrade:NO];
    }
    return self;
}

- (void)dealloc {
    [_timer invalidate];
    [_timer release];
    _menu.delegate = nil;
    [[NSStatusBar systemStatusBar] removeStatusItem:_statusItem];
    [_statusItem release];
    [_menu release];
    [_items release];
    [super dealloc];
}

- (NSMenuItem *)item:(BattMenuItem)item {
    return [_items objectForKey:[NSNumber numberWithInteger:item]];
}

- (void)rememberItem:(NSMenuItem *)item as:(BattMenuItem)identifier {
    [_items setObject:item forKey:[NSNumber numberWithInteger:identifier]];
}

- (void)menuAction:(NSMenuItem *)sender {
    BattMenuItem identifier = (BattMenuItem)sender.tag;
    switch (identifier) {
        case BattItemMagSafeEnabled:
        case BattItemMagSafeDisabled:
        case BattItemMagSafeAlwaysOff:
            [self item:BattItemMagSafeEnabled].state = identifier == BattItemMagSafeEnabled
                ? NSControlStateValueOn : NSControlStateValueOff;
            [self item:BattItemMagSafeDisabled].state = identifier == BattItemMagSafeDisabled
                ? NSControlStateValueOn : NSControlStateValueOff;
            [self item:BattItemMagSafeAlwaysOff].state = identifier == BattItemMagSafeAlwaysOff
                ? NSControlStateValueOn : NSControlStateValueOff;
            break;
        case BattItemPreventIdleSleep:
        case BattItemDisableChargingPreSleep:
        case BattItemPreventSystemSleep:
        case BattItemForceDischarge:
            sender.state = sender.state == NSControlStateValueOff
                ? NSControlStateValueOn : NSControlStateValueOff;
            break;
        default:
            break;
    }
    battMenuAction(_handle, identifier, sender.state == NSControlStateValueOn);
}

- (void)noop:(NSMenuItem *)sender {
    (void)sender;
}

- (void)menuWillOpen:(NSMenu *)menu {
    (void)menu;
    battMenuWillOpen(_handle);
    self.timer = [NSTimer timerWithTimeInterval:BattMenuUpdateInterval
                                        target:self
                                      selector:@selector(timerTick:)
                                      userInfo:nil
                                       repeats:YES];
    [[NSRunLoop mainRunLoop] addTimer:self.timer forMode:NSRunLoopCommonModes];
}

- (void)menuDidClose:(NSMenu *)menu {
    (void)menu;
    [self.timer invalidate];
    self.timer = nil;
}

- (void)timerTick:(NSTimer *)timer {
    (void)timer;
    battMenuTimerFired(_handle);
}

- (void)setStatusIconInstalled:(BOOL)installed
                       capable:(BOOL)capable
                  needsUpgrade:(BOOL)needsUpgrade {
    NSString *symbol = @"minus.plus.batteryblock";
    NSString *description = @"batt icon";
    if (!installed) {
        symbol = @"batteryblock.slash";
        description = @"batt daemon not installed";
    } else if (!capable) {
        symbol = @"minus.plus.batteryblock.exclamationmark";
        description = @"Your machine cannot run batt";
    } else if (needsUpgrade) {
        symbol = @"fluid.batteryblock";
        description = @"batt needs upgrade";
    }
    self.statusItem.button.image = [NSImage imageWithSystemSymbolName:symbol
                                            accessibilityDescription:description];
}

- (void)setPowerItem:(BattMenuItem)item label:(NSString *)label value:(double)value {
    NSColor *color = NSColor.labelColor;
    char sign = ' ';
    if (![label isEqualToString:@"System"]) {
        if (value > 0) {
            color = NSColor.systemGreenColor;
            sign = '+';
        } else if (value < 0) {
            color = NSColor.systemRedColor;
            sign = '-';
        }
    }

    NSString *labelWithColon = [label stringByAppendingString:@":"];
    NSString *text = [NSString stringWithFormat:@"%-8s %c%7.2fW",
                      labelWithColon.UTF8String, sign, fabs(value)];
    NSMutableAttributedString *attributed = [[[NSMutableAttributedString alloc]
        initWithString:text] autorelease];
    NSRange labelRange = NSMakeRange(0, 9);
    NSRange valueRange = NSMakeRange(9, text.length - 9);
    [attributed addAttribute:NSForegroundColorAttributeName
                       value:NSColor.secondaryLabelColor
                       range:labelRange];
    [attributed addAttribute:NSForegroundColorAttributeName value:color range:valueRange];
    [attributed addAttribute:NSFontAttributeName
                       value:[NSFont monospacedSystemFontOfSize:12 weight:NSFontWeightRegular]
                       range:NSMakeRange(0, text.length)];
    [self item:item].attributedTitle = attributed;
}

@end

void BattBuildMenu(BattMenuController *controller, NSString *version) {
    NSMenu *root = controller.menu;
    NSMenu *power = AddSubmenu(controller, root, @"Power Flow", BattItemPowerFlow);
    [power addItem:DisplayItem(controller, @"", BattItemPowerSystem, YES)];
    [power addItem:DisplayItem(controller, @"", BattItemPowerAdapter, YES)];
    [power addItem:DisplayItem(controller, @"", BattItemPowerBattery, YES)];
    [controller setPowerItem:BattItemPowerSystem label:@"System" value:0];
    [controller setPowerItem:BattItemPowerAdapter label:@"Adapter" value:0];
    [controller setPowerItem:BattItemPowerBattery label:@"Battery" value:0];

    [root addItem:ActionItem(controller, @"Upgrade Daemon...", @"u", BattItemUpgrade)];
    [root addItem:ActionItem(controller, @"Install Daemon...", @"i", BattItemInstall)];
    [root addItem:DisplayItem(controller, @"Loading...", BattItemState, NO)];
    [root addItem:DisplayItem(controller, @"Loading...", BattItemCurrentLimit, NO)];
    [root addItem:[NSMenuItem separatorItem]];
    [root addItem:DisplayItem(controller, @"Quick Limits", BattItemQuickLimits, NO)];

    const NSInteger limits[] = {50, 60, 70, 80, 90};
    const BattMenuItem limitItems[] = {
        BattItemLimit50, BattItemLimit60, BattItemLimit70, BattItemLimit80, BattItemLimit90,
    };
    for (NSUInteger index = 0; index < 5; index++) {
        NSInteger limit = limits[index];
        NSString *title = [NSString stringWithFormat:@"Set %ld%% Limit", (long)limit];
        NSString *key = [NSString stringWithFormat:@"%ld", (long)limit];
        [root addItem:ActionItem(controller, title, key, limitItems[index])];
    }

    [root addItem:[NSMenuItem separatorItem]];
    NSMenu *advanced = AddSubmenu(controller, root, @"Advanced", BattItemAdvanced);
    NSMenu *magSafe = AddSubmenu(controller, advanced, @"Control MagSafe LED", BattItemMagSafe);
    [magSafe addItem:ActionItem(controller, @"Enable", @"", BattItemMagSafeEnabled)];
    [magSafe addItem:ActionItem(controller, @"Disable", @"", BattItemMagSafeDisabled)];
    [magSafe addItem:ActionItem(controller, @"Always-off", @"", BattItemMagSafeAlwaysOff)];

    [advanced addItem:ActionItem(controller, @"Prevent Idle Sleep when Charging", @"",
                                  BattItemPreventIdleSleep)];
    [advanced addItem:ActionItem(controller, @"Disable Charging before Sleep", @"",
                                  BattItemDisableChargingPreSleep)];
    [advanced addItem:ActionItem(controller,
                                  @"Prevent System Sleep when Charging (Experimental)", @"",
                                  BattItemPreventSystemSleep)];
    [advanced addItem:ActionItem(controller, @"Force Discharge...", @"",
                                  BattItemForceDischarge)];

    NSMenu *calibration = AddSubmenu(controller, advanced,
                                     @"Auto Calibration (Experimental)...",
                                     BattItemAutoCalibration);
    [calibration addItem:DisplayItem(controller, @"Status: Idle",
                                      BattItemCalibrationStatus, NO)];
    [calibration addItem:ActionItem(controller, @"Start", @"", BattItemCalibrationStart)];
    [calibration addItem:ActionItem(controller, @"Pause", @"", BattItemCalibrationPause)];
    [calibration addItem:ActionItem(controller, @"Resume", @"", BattItemCalibrationResume)];
    [calibration addItem:ActionItem(controller, @"Cancel", @"", BattItemCalibrationCancel)];

    [advanced addItem:[NSMenuItem separatorItem]];
    NSString *versionTitle = [@"Version: " stringByAppendingString:version ?: @""];
    [advanced addItem:DisplayItem(controller, versionTitle, BattItemVersion, NO)];
    [advanced addItem:ActionItem(controller, @"Uninstall Daemon...", @"", BattItemUninstall)];

    [root addItem:[NSMenuItem separatorItem]];
    [root addItem:ActionItem(controller, @"Disable Charging Limit", @"d", BattItemDisableLimit)];
    [root addItem:ActionItem(controller, @"Quit Menubar App", @"q", BattItemQuit)];
}
