package daemon

/*
#cgo LDFLAGS: -framework CoreFoundation -framework IOKit

#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>

// battDictInt returns the integer stored under key in dict, or 0 when the key
// is missing or does not hold an integer.
static long long battDictInt(CFDictionaryRef dict, CFStringRef key) {
	long long value = 0;
	CFTypeRef ref = CFDictionaryGetValue(dict, key);
	if (ref == NULL || CFGetTypeID(ref) != CFNumberGetTypeID() ||
	    !CFNumberGetValue((CFNumberRef)ref, kCFNumberSInt64Type, &value)) {
		return 0;
	}
	return value;
}

// battReadBatteryData copies the mAh capacities out of the BatteryData
// dictionary of the AppleSmartBattery IORegistry entry. It returns 0 on
// success, 1 when there is no AppleSmartBattery and 2 when the entry has no
// BatteryData dictionary.
static int battReadBatteryData(long long *design, long long *rawMax) {
	io_service_t battery = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("AppleSmartBattery"));
	if (battery == IO_OBJECT_NULL) {
		return 1;
	}
	CFTypeRef data = IORegistryEntryCreateCFProperty(battery, CFSTR("BatteryData"), kCFAllocatorDefault, 0);
	IOObjectRelease(battery);
	if (data == NULL) {
		return 2;
	}
	if (CFGetTypeID(data) != CFDictionaryGetTypeID()) {
		CFRelease(data);
		return 2;
	}
	*design = battDictInt((CFDictionaryRef)data, CFSTR("DesignCapacity"));
	*rawMax = battDictInt((CFDictionaryRef)data, CFSTR("AppleRawMaxCapacity"));
	CFRelease(data);
	return 0;
}
*/
import "C"

import "errors"

// readBatteryDataCapacities reads the mAh capacities from the BatteryData
// dictionary of the AppleSmartBattery IORegistry entry. A capacity missing
// from the dictionary is 0. Before macOS 27 the dictionary has
// DesignCapacity but not AppleRawMaxCapacity.
func readBatteryDataCapacities() (batteryCapacities, error) {
	var design, rawMax C.longlong
	switch C.battReadBatteryData(&design, &rawMax) {
	case 0:
		return batteryCapacities{Design: int(design), Max: int(rawMax)}, nil
	case 1:
		return batteryCapacities{}, errors.New("no AppleSmartBattery in the IORegistry")
	default:
		return batteryCapacities{}, errors.New("AppleSmartBattery has no BatteryData dictionary")
	}
}
