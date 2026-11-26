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
        case BattItemPreventSleepOnAdapterDisable:
        case BattItemNoDrain:
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
    NSString *description = NSLocalizedString(@"batt icon", nil);
    if (!installed) {
        symbol = @"batteryblock.slash";
        description = NSLocalizedString(@"batt daemon not installed", nil);
    } else if (!capable) {
        symbol = @"minus.plus.batteryblock.exclamationmark";
        description = NSLocalizedString(@"Your machine cannot run batt", nil);
    } else if (needsUpgrade) {
        symbol = @"fluid.batteryblock";
        description = NSLocalizedString(@"batt needs upgrade", nil);
    }
    self.statusItem.button.image = [NSImage imageWithSystemSymbolName:symbol
                                            accessibilityDescription:description];
}

- (void)setPowerItem:(BattMenuItem)item label:(NSString *)label value:(double)value {
    NSColor *color = NSColor.labelColor;
    char sign = ' ';
    // Compare by enum instead of label string so localization does not break color styling.
    if (item != BattItemPowerSystem) {
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
    NSMenu *power = AddSubmenu(controller, root, NSLocalizedString(@"Power Flow", nil), BattItemPowerFlow);
    [power addItem:DisplayItem(controller, @"", BattItemPowerSystem, YES)];
    [power addItem:DisplayItem(controller, @"", BattItemPowerAdapter, YES)];
    [power addItem:DisplayItem(controller, @"", BattItemPowerBattery, YES)];
    [controller setPowerItem:BattItemPowerSystem label:NSLocalizedString(@"System", nil) value:0];
    [controller setPowerItem:BattItemPowerAdapter label:NSLocalizedString(@"Adapter", nil) value:0];
    [controller setPowerItem:BattItemPowerBattery label:NSLocalizedString(@"Battery", nil) value:0];

    [root addItem:ActionItem(controller, NSLocalizedString(@"Upgrade Daemon...", nil), @"u", BattItemUpgrade)];
    [root addItem:ActionItem(controller, NSLocalizedString(@"Install Daemon...", nil), @"i", BattItemInstall)];
    [root addItem:DisplayItem(controller, NSLocalizedString(@"Loading...", nil), BattItemState, NO)];
    [root addItem:DisplayItem(controller, NSLocalizedString(@"Loading...", nil), BattItemCurrentLimit, NO)];
    NSMenuItem *nativeLimitNotice = DisplayItem(controller, @"", BattItemNativeLimitNotice, NO);
    nativeLimitNotice.hidden = YES;
    [root addItem:nativeLimitNotice];
    [root addItem:[NSMenuItem separatorItem]];
    [root addItem:DisplayItem(controller, NSLocalizedString(@"Quick Limits", nil), BattItemQuickLimits, NO)];

    const NSInteger limits[] = {50, 60, 70, 80, 90};
    const BattMenuItem limitItems[] = {
        BattItemLimit50, BattItemLimit60, BattItemLimit70, BattItemLimit80, BattItemLimit90,
    };
    for (NSUInteger index = 0; index < 5; index++) {
        NSInteger limit = limits[index];
        NSString *title = [NSString stringWithFormat:NSLocalizedString(@"Set %ld%% Limit", nil), (long)limit];
        NSString *key = [NSString stringWithFormat:@"%ld", (long)limit];
        [root addItem:ActionItem(controller, title, key, limitItems[index])];
    }

    [root addItem:[NSMenuItem separatorItem]];
    [root addItem:DisplayItem(controller, NSLocalizedString(@"One-Time Charge", nil), BattItemChargeOnce, NO)];
    [root addItem:ActionItem(controller, NSLocalizedString(@"Charge to Limit Now", nil), @"",
                              BattItemChargeOnceLimit)];
    [root addItem:ActionItem(controller, NSLocalizedString(@"Charge to Full Once", nil), @"",
                              BattItemChargeOnceFull)];
    NSMenuItem *chargeOnceStatus = DisplayItem(controller, @"", BattItemChargeOnceStatus, NO);
    chargeOnceStatus.hidden = YES;
    [root addItem:chargeOnceStatus];
    NSMenuItem *chargeOnceCancel = ActionItem(controller, NSLocalizedString(@"Cancel One-Time Charge", nil), @"",
                                              BattItemChargeOnceCancel);
    chargeOnceCancel.hidden = YES;
    [root addItem:chargeOnceCancel];

    [root addItem:[NSMenuItem separatorItem]];
    NSMenu *advanced = AddSubmenu(controller, root, NSLocalizedString(@"Advanced", nil), BattItemAdvanced);
    NSMenu *magSafe = AddSubmenu(controller, advanced, NSLocalizedString(@"Control MagSafe LED", nil), BattItemMagSafe);
    [magSafe addItem:ActionItem(controller, NSLocalizedString(@"Enable", nil), @"", BattItemMagSafeEnabled)];
    [magSafe addItem:ActionItem(controller, NSLocalizedString(@"Disable", nil), @"", BattItemMagSafeDisabled)];
    [magSafe addItem:ActionItem(controller, NSLocalizedString(@"Always-off", nil), @"", BattItemMagSafeAlwaysOff)];

    [advanced addItem:ActionItem(controller, NSLocalizedString(@"Prevent Idle Sleep when Charging", nil), @"",
                                  BattItemPreventIdleSleep)];
    [advanced addItem:ActionItem(controller, NSLocalizedString(@"Disable Charging before Sleep", nil), @"",
                                  BattItemDisableChargingPreSleep)];
    [advanced addItem:ActionItem(controller,
                                  NSLocalizedString(@"Prevent System Sleep when Charging (Experimental)", nil), @"",
                                  BattItemPreventSystemSleep)];
    [advanced addItem:ActionItem(controller,
                                  NSLocalizedString(@"Prevent Sleep when Adapter is Disabled", nil), @"",
                                  BattItemPreventSleepOnAdapterDisable)];
    [advanced addItem:ActionItem(controller,
                                  NSLocalizedString(@"No-drain Above Native Limit (Experimental)", nil), @"",
                                  BattItemNoDrain)];
    NSMenu *forceDischarge = AddSubmenu(controller, advanced, NSLocalizedString(@"Force Discharge...", nil),
                                        BattItemForceDischarge);
    NSMenuItem *forceDischargeCountdown = DisplayItem(controller, @"",
                                                       BattItemForceDischargeCountdown, NO);
    forceDischargeCountdown.hidden = YES;
    [forceDischarge addItem:forceDischargeCountdown];
    [forceDischarge addItem:ActionItem(controller, NSLocalizedString(@"Stop Force Discharge", nil), @"",
                                        BattItemForceDischargeStop)];
    [forceDischarge addItem:ActionItem(controller, NSLocalizedString(@"Indefinitely", nil), @"",
                                        BattItemForceDischargeIndefinitely)];
    [forceDischarge addItem:[NSMenuItem separatorItem]];
    [forceDischarge addItem:ActionItem(controller, NSLocalizedString(@"1 Hour", nil), @"",
                                        BattItemForceDischarge1Hour)];
    [forceDischarge addItem:ActionItem(controller, NSLocalizedString(@"2 Hours", nil), @"",
                                        BattItemForceDischarge2Hours)];
    [forceDischarge addItem:ActionItem(controller, NSLocalizedString(@"4 Hours", nil), @"",
                                        BattItemForceDischarge4Hours)];
    [forceDischarge addItem:ActionItem(controller, NSLocalizedString(@"8 Hours", nil), @"",
                                        BattItemForceDischarge8Hours)];

    NSMenu *calibration = AddSubmenu(controller, advanced,
                                     NSLocalizedString(@"Auto Calibration (Experimental)...", nil),
                                     BattItemAutoCalibration);
    [calibration addItem:DisplayItem(controller, NSLocalizedString(@"Status: Idle", nil),
                                      BattItemCalibrationStatus, NO)];
    [calibration addItem:ActionItem(controller, NSLocalizedString(@"Start", nil), @"", BattItemCalibrationStart)];
    [calibration addItem:ActionItem(controller, NSLocalizedString(@"Pause", nil), @"", BattItemCalibrationPause)];
    [calibration addItem:ActionItem(controller, NSLocalizedString(@"Resume", nil), @"", BattItemCalibrationResume)];
    [calibration addItem:ActionItem(controller, NSLocalizedString(@"Cancel", nil), @"", BattItemCalibrationCancel)];

    [advanced addItem:[NSMenuItem separatorItem]];
    NSString *versionTitle = [NSString stringWithFormat:@"%@ %@",
                              NSLocalizedString(@"Version:", nil), version ?: @""];
    [advanced addItem:DisplayItem(controller, versionTitle, BattItemVersion, NO)];
    [advanced addItem:ActionItem(controller, NSLocalizedString(@"Uninstall Daemon...", nil), @"", BattItemUninstall)];

    [root addItem:[NSMenuItem separatorItem]];
    NSMenu *disableLimit = AddSubmenu(controller, root, NSLocalizedString(@"Disable Charge Limit", nil),
                                      BattItemDisableLimit);
    NSMenuItem *disableCountdown = DisplayItem(controller, @"",
                                                BattItemDisableLimitCountdown, NO);
    disableCountdown.hidden = YES;
    [disableLimit addItem:disableCountdown];
    [disableLimit addItem:ActionItem(controller, NSLocalizedString(@"Indefinitely", nil), @"d",
                                      BattItemDisableLimitIndefinitely)];
    [disableLimit addItem:[NSMenuItem separatorItem]];
    [disableLimit addItem:ActionItem(controller, NSLocalizedString(@"1 Hour", nil), @"",
                                      BattItemDisableLimit1Hour)];
    [disableLimit addItem:ActionItem(controller, NSLocalizedString(@"2 Hours", nil), @"",
                                      BattItemDisableLimit2Hours)];
    [disableLimit addItem:ActionItem(controller, NSLocalizedString(@"4 Hours", nil), @"",
                                      BattItemDisableLimit4Hours)];
    [disableLimit addItem:ActionItem(controller, NSLocalizedString(@"8 Hours", nil), @"",
                                      BattItemDisableLimit8Hours)];
    [disableLimit addItem:ActionItem(controller, NSLocalizedString(@"12 Hours", nil), @"",
                                      BattItemDisableLimit12Hours)];
    [disableLimit addItem:ActionItem(controller, NSLocalizedString(@"24 Hours", nil), @"",
                                      BattItemDisableLimit24Hours)];
    [disableLimit addItem:ActionItem(controller, NSLocalizedString(@"2 Days", nil), @"",
                                      BattItemDisableLimit2Days)];
    [disableLimit addItem:ActionItem(controller, NSLocalizedString(@"3 Days", nil), @"",
                                      BattItemDisableLimit3Days)];
    [disableLimit addItem:ActionItem(controller, NSLocalizedString(@"7 Days", nil), @"",
                                      BattItemDisableLimit7Days)];
    [root addItem:ActionItem(controller, NSLocalizedString(@"Quit Menubar App", nil), @"q", BattItemQuit)];
}
