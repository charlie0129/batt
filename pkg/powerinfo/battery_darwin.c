#include "battery.h"

static long long battery_number(CFDictionaryRef properties, CFDictionaryRef details, CFStringRef key) {
    CFTypeRef value = CFDictionaryGetValue(properties, key);
    if ((!value || CFGetTypeID(value) != CFNumberGetTypeID()) && details)
        value = CFDictionaryGetValue(details, key);
    long long result = 0;
    if (value && CFGetTypeID(value) == CFNumberGetTypeID())
        CFNumberGetValue(value, kCFNumberLongLongType, &result);
    return result;
}

static void decode_battery_snapshot(CFDictionaryRef properties, batt_battery_snapshot *snapshot) {
    CFTypeRef details = CFDictionaryGetValue(properties, CFSTR("BatteryData"));
    if (details && CFGetTypeID(details) != CFDictionaryGetTypeID()) details = NULL;
    snapshot->state = CFDictionaryGetValue(properties, CFSTR("FullyCharged")) == kCFBooleanTrue ? 2 :
        (CFDictionaryGetValue(properties, CFSTR("IsCharging")) == kCFBooleanTrue ? 1 : 0);
    snapshot->designCapacity = battery_number(properties, details, CFSTR("DesignCapacity"));
    snapshot->maxCapacity = battery_number(properties, details, CFSTR("AppleRawMaxCapacity"));
    snapshot->amperage = battery_number(properties, details, CFSTR("Amperage"));
    snapshot->voltage = battery_number(properties, details, CFSTR("Voltage"));
}

kern_return_t batt_read_battery(batt_battery_snapshot *snapshot) {
    io_service_t service = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("AppleSmartBattery"));
    if (!service) return kIOReturnNotFound;
    CFMutableDictionaryRef properties = NULL;
    kern_return_t result = IORegistryEntryCreateCFProperties(service, &properties, kCFAllocatorDefault, 0);
    IOObjectRelease(service);
    if (result == KERN_SUCCESS && properties) decode_battery_snapshot(properties, snapshot);
    else if (result == KERN_SUCCESS) result = kIOReturnNotFound;
    if (properties) CFRelease(properties);
    return result;
}
