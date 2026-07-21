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
        if (confirmation == BattConfirmationForceDischarge ||
            confirmation == BattConfirmationForceDischargeIndefinitely) {
            alert.icon = [NSImage imageWithSystemSymbolName:@"note.text"
                                  accessibilityDescription:@"notes"];
            alert.messageText = @"Precautions";
            NSString *text =
                @"1. The lid of your MacBook MUST be open, otherwise your Mac will go to sleep immediately.\n"
                 "2. Force Discharge cuts wall power, making your Mac run on battery power just as if it were unplugged. If the battery charge is exhausted, your Mac will shut down.";
            if (confirmation == BattConfirmationForceDischargeIndefinitely) {
                text = [text stringByAppendingString:
                    @"\n3. An indefinite force discharge will continue until you stop it. Prefer a timed duration unless you specifically need indefinite discharge."];
            }
            alert.informativeText = text;
        } else if (confirmation == BattConfirmationStartCalibration) {
            alert.icon = [NSImage imageWithSystemSymbolName:@"battery.100"
                                  accessibilityDescription:@"calibration"];
            alert.messageText = @"Start Auto Calibration?";
            alert.informativeText =
                @"This will:\n"
                 "1. Discharge to 15% by default.\n"
                 "2. Charge to 100%.\n"
                 "3. Hold at full charge (for 2 hours by default).\n"
                 "4. Discharge back to previous charge limit.\n\n"
                 "NOTES:\n"
                 "• batt will prevent idle sleep until calibration finishes, is cancelled, or fails.\n"
                 "• You can pause or cancel anytime from the menu.\n"
                 "• Highly recommend keeping your Mac connected to power throughout the process to prevent the battery level from dropping below the threshold without timely charging.\n"
                 "• Closing the lid or explicitly choosing Sleep can still force sleep, so keep the lid open during calibration.";
        } else {
            return false;
        }
        [alert addButtonWithTitle:@"Start"];
        [alert addButtonWithTitle:@"Cancel"];
        return [alert runModal] == NSAlertFirstButtonReturn;
    }
}
