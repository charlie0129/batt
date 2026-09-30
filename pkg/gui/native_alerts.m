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
                @"1. Keep the lid open unless \"Prevent Sleep when Adapter is Disabled\" is enabled. When using that option with the lid closed, keep your Mac monitored and never place it in a bag.\n"
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
                 "• Closing the lid or explicitly choosing Sleep can still force sleep unless \"Prevent Sleep when Adapter is Disabled\" is enabled. When using that option with the lid closed, keep your Mac monitored and never place it in a bag.";
        } else if (confirmation == BattConfirmationPreventSleepOnAdapterDisable) {
            alert.alertStyle = NSAlertStyleWarning;
            alert.icon = [NSImage imageWithSystemSymbolName:@"exclamationmark.triangle"
                                  accessibilityDescription:@"Warning"];
            alert.messageText = @"Prevent Sleep when Adapter is Disabled?";
            alert.informativeText =
                @"This enables macOS’s global no-sleep setting while adapter input is disabled. Your Mac may remain awake with the lid closed, overheat in a bag, or drain its battery. Use this only when the Mac is monitored. Continue?";
        } else {
            return false;
        }
        if (confirmation == BattConfirmationPreventSleepOnAdapterDisable) {
            [alert addButtonWithTitle:@"Continue"];
            [alert addButtonWithTitle:@"Cancel"];
        } else {
            [alert addButtonWithTitle:@"Start"];
            [alert addButtonWithTitle:@"Cancel"];
        }
        return [alert runModal] == NSAlertFirstButtonReturn;
    }
}
