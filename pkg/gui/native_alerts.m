#import <Cocoa/Cocoa.h>

#include "native.h"

static NSString *AlertString(const char *value) {
    return value == NULL ? @"" : [NSString stringWithUTF8String:value];
}

void batt_show_alert(const char *message, const char *body) {
    @autoreleasepool {
        NSAlert *alert = [[[NSAlert alloc] init] autorelease];
        alert.icon = [NSImage imageWithSystemSymbolName:@"exclamationmark.triangle"
                              accessibilityDescription:@"Warning"];
        alert.alertStyle = NSAlertStyleWarning;
        alert.messageText = AlertString(message);
        alert.informativeText = AlertString(body);
        [alert runModal];
    }
}

bool batt_show_confirmation(int confirmation) {
    @autoreleasepool {
        NSAlert *alert = [[[NSAlert alloc] init] autorelease];
        alert.alertStyle = NSAlertStyleInformational;
        if (confirmation == BattConfirmationForceDischarge) {
            alert.icon = [NSImage imageWithSystemSymbolName:@"note.text"
                                  accessibilityDescription:@"notes"];
            alert.messageText = @"Precautions";
            alert.informativeText =
                @"1. The lid of your MacBook MUST be open, otherwise your Mac will go to sleep immediately.\n"
                 "2. Be sure to come back and disable \"Force Discharge\" when you are done, otherwise the battery of your Mac will drain completely.";
        } else if (confirmation == BattConfirmationStartCalibration) {
            alert.icon = [NSImage imageWithSystemSymbolName:@"battery.100"
                                  accessibilityDescription:@"calibration"];
            alert.messageText = @"Start Auto Calibration?";
            alert.informativeText =
                @"This will:\n"
                 "1. Discharge (to 15% by default) without sleep prevention.\n"
                 "2. Charge to 100%.\n"
                 "3. Hold at full charge (for 2 hours by default).\n"
                 "4. Discharge back to previous charge limit.\n\n"
                 "NOTES:\n"
                 "• You can pause or cancel anytime from the menu.\n"
                 "• Highly recommend keeping your Mac connected to power throughout the process to prevent the battery level from dropping below the threshold without timely charging.\n"
                 "• If you are using Clamshell mode (using a Mac laptop with an external monitor and the lid closed), *the discharging process will cause your Mac to go to sleep*. So you should keep the lid open during the calibration process.";
        } else {
            return false;
        }
        [alert addButtonWithTitle:@"Start"];
        [alert addButtonWithTitle:@"Cancel"];
        return [alert runModal] == NSAlertFirstButtonReturn;
    }
}
