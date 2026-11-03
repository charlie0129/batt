#pragma once
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>

typedef struct {
    int state;
    long long designCapacity, maxCapacity, amperage, voltage;
} batt_battery_snapshot;

kern_return_t batt_read_battery(batt_battery_snapshot *snapshot);
