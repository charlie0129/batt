#import <Foundation/Foundation.h>
#include <assert.h>
#include "../battery_darwin.c"

int main(void) { @autoreleasepool {
    batt_battery_snapshot snapshot;
    NSDictionary *properties = @{@"DesignCapacity":@6000, @"AppleRawMaxCapacity":@5500,
        @"Amperage":@-1000, @"Voltage":@12000, @"IsCharging":@NO};
    decode_battery_snapshot((CFDictionaryRef)properties, &snapshot);
    assert(snapshot.state == 0 && snapshot.designCapacity == 6000 && snapshot.maxCapacity == 5500);
    assert(snapshot.amperage == -1000 && snapshot.voltage == 12000);
    // macOS 27 moved capacity fields into BatteryData. Full takes priority over charging.
    properties = @{@"BatteryData":@{@"DesignCapacity":@6200, @"AppleRawMaxCapacity":@5800},
        @"Amperage":@2000, @"Voltage":@12500, @"IsCharging":@YES, @"FullyCharged":@YES};
    decode_battery_snapshot((CFDictionaryRef)properties, &snapshot);
    assert(snapshot.state == 2 && snapshot.designCapacity == 6200 && snapshot.maxCapacity == 5800);
    assert(snapshot.amperage == 2000 && snapshot.voltage == 12500);
} return 0; }
