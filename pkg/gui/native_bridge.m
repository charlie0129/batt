#import "native_internal.h"

static NSString *BattString(const char *value) {
    return value == NULL ? @"" : [NSString stringWithUTF8String:value];
}

static BattMenuController *BattController(BattMenuRef menu) {
    return (BattMenuController *)menu;
}

BattMenuRef batt_menu_create(uintptr_t handle, const char *version) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        return [[BattMenuController alloc] initWithHandle:handle version:BattString(version)];
    }
}

void batt_menu_destroy(BattMenuRef menu) {
    @autoreleasepool {
        [BattController(menu) release];
    }
}

void batt_app_run(void) {
    @autoreleasepool {
        [[NSApplication sharedApplication] run];
    }
}

void batt_app_terminate(void) {
    @autoreleasepool {
        [[NSApplication sharedApplication] terminate:nil];
    }
}

void batt_menu_set_title(BattMenuRef menu, int item, const char *title) {
    @autoreleasepool {
        [BattController(menu) item:(BattMenuItem)item].title = BattString(title);
    }
}

void batt_menu_set_tooltip(BattMenuRef menu, int item, const char *tooltip) {
    @autoreleasepool {
        [BattController(menu) item:(BattMenuItem)item].toolTip = BattString(tooltip);
    }
}

void batt_menu_set_hidden(BattMenuRef menu, int item, bool hidden) {
    [BattController(menu) item:(BattMenuItem)item].hidden = hidden;
}

void batt_menu_set_enabled(BattMenuRef menu, int item, bool enabled) {
    [BattController(menu) item:(BattMenuItem)item].enabled = enabled;
}

void batt_menu_set_checked(BattMenuRef menu, int item, bool checked) {
    [BattController(menu) item:(BattMenuItem)item].state = checked
        ? NSControlStateValueOn : NSControlStateValueOff;
}

void batt_menu_set_status_icon(BattMenuRef menu,
                               bool installed,
                               bool capable,
                               bool needs_upgrade) {
    @autoreleasepool {
        [BattController(menu) setStatusIconInstalled:installed
                                            capable:capable
                                       needsUpgrade:needs_upgrade];
    }
}

void batt_menu_set_power(BattMenuRef menu, int item, const char *label, double value) {
    @autoreleasepool {
        [BattController(menu) setPowerItem:(BattMenuItem)item label:BattString(label) value:value];
    }
}
