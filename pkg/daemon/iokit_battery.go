package daemon

/*
#cgo LDFLAGS: -framework CoreFoundation -framework IOKit

#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>

// battCopyBattery returns the AppleSmartBattery IORegistry entry, or
// IO_OBJECT_NULL when the Mac has none. The caller releases it.
static io_registry_entry_t battCopyBattery(void) {
	CFMutableDictionaryRef matching = IOServiceMatching("AppleSmartBattery");
	if (matching == NULL) {
		return IO_OBJECT_NULL;
	}
	// IOServiceGetMatchingService consumes matching.
	return IOServiceGetMatchingService(kIOMainPortDefault, matching);
}

// battNumber returns ref as an integer, or 0 when ref is not one.
static long long battNumber(CFTypeRef ref) {
	long long value = 0;
	if (ref == NULL || CFGetTypeID(ref) != CFNumberGetTypeID() ||
	    !CFNumberGetValue((CFNumberRef)ref, kCFNumberSInt64Type, &value)) {
		return 0;
	}
	return value;
}

// battDictInt returns the integer stored under key in dict, or 0 when the key
// is missing or does not hold an integer.
static long long battDictInt(CFDictionaryRef dict, CFStringRef key) {
	return battNumber(CFDictionaryGetValue(dict, key));
}

// battEntryInt returns the integer property key of entry, or 0 when the
// entry lacks it or it is not an integer.
static long long battEntryInt(io_registry_entry_t entry, CFStringRef key) {
	CFTypeRef ref = IORegistryEntryCreateCFProperty(entry, key, kCFAllocatorDefault, 0);
	long long value = battNumber(ref);
	if (ref != NULL) {
		CFRelease(ref);
	}
	return value;
}

// battEntryBool returns the boolean property key of entry, or 0 when the
// entry lacks it or it is not a boolean.
static int battEntryBool(io_registry_entry_t entry, CFStringRef key) {
	CFTypeRef ref = IORegistryEntryCreateCFProperty(entry, key, kCFAllocatorDefault, 0);
	if (ref == NULL) {
		return 0;
	}
	int value = CFGetTypeID(ref) == CFBooleanGetTypeID() && CFBooleanGetValue((CFBooleanRef)ref);
	CFRelease(ref);
	return value;
}

// battEntryDict returns the dictionary property key of entry, or NULL when the
// entry lacks it or it is not a dictionary. The caller releases it.
static CFDictionaryRef battEntryDict(io_registry_entry_t entry, CFStringRef key) {
	CFTypeRef ref = IORegistryEntryCreateCFProperty(entry, key, kCFAllocatorDefault, 0);
	if (ref != NULL && CFGetTypeID(ref) != CFDictionaryGetTypeID()) {
		CFRelease(ref);
		return NULL;
	}
	return (CFDictionaryRef)ref;
}

typedef struct {
	int is_charging;
	int fully_charged;
	long long voltage;
	long long amperage;
	long long cycle_count;
	long long design_capacity;
	long long raw_max_capacity;
	long long system_voltage_in;
	long long system_current_in;
} battSmartBattery;

// battReadSmartBattery reads the properties of the AppleSmartBattery entry
// that batt uses. It returns 0 on success and 1 when there is no
// AppleSmartBattery.
static int battReadSmartBattery(battSmartBattery *out) {
	io_registry_entry_t battery = battCopyBattery();
	if (battery == IO_OBJECT_NULL) {
		return 1;
	}
	out->is_charging = battEntryBool(battery, CFSTR("IsCharging"));
	out->fully_charged = battEntryBool(battery, CFSTR("FullyCharged"));
	out->voltage = battEntryInt(battery, CFSTR("Voltage"));
	out->amperage = battEntryInt(battery, CFSTR("Amperage"));
	out->cycle_count = battEntryInt(battery, CFSTR("CycleCount"));
	out->design_capacity = battEntryInt(battery, CFSTR("DesignCapacity"));
	out->raw_max_capacity = battEntryInt(battery, CFSTR("AppleRawMaxCapacity"));
	CFDictionaryRef telemetry = battEntryDict(battery, CFSTR("PowerTelemetryData"));
	if (telemetry != NULL) {
		out->system_voltage_in = battDictInt(telemetry, CFSTR("SystemVoltageIn"));
		out->system_current_in = battDictInt(telemetry, CFSTR("SystemCurrentIn"));
		CFRelease(telemetry);
	}
	IOObjectRelease(battery);
	return 0;
}

// battReadBatteryData copies the mAh capacities out of the BatteryData
// dictionary of the AppleSmartBattery entry. It returns 0 on success, 1 when
// there is no AppleSmartBattery and 2 when the entry has no BatteryData
// dictionary.
static int battReadBatteryData(long long *design, long long *rawMax) {
	io_registry_entry_t battery = battCopyBattery();
	if (battery == IO_OBJECT_NULL) {
		return 1;
	}
	CFDictionaryRef data = battEntryDict(battery, CFSTR("BatteryData"));
	IOObjectRelease(battery);
	if (data == NULL) {
		return 2;
	}
	*design = battDictInt(data, CFSTR("DesignCapacity"));
	*rawMax = battDictInt(data, CFSTR("AppleRawMaxCapacity"));
	CFRelease(data);
	return 0;
}
*/
import "C"

import "errors"

var errNoSmartBattery = errors.New("no AppleSmartBattery in the IORegistry")

// smartBattery is what the AppleSmartBattery IORegistry entry reports, in its
// own units. A property the entry lacks reads as 0.
type smartBattery struct {
	IsCharging     bool
	FullyCharged   bool
	Voltage        int // mV
	Amperage       int // mA, negative while discharging
	CycleCount     int
	DesignCapacity int // mAh
	MaxCapacity    int // mAh, AppleRawMaxCapacity
	// SystemVoltageIn and SystemCurrentIn come from PowerTelemetryData: what
	// the Mac draws from the power adapter, in mV and mA.
	SystemVoltageIn int
	SystemCurrentIn int
}

// readSmartBattery reads the AppleSmartBattery IORegistry entry.
func readSmartBattery() (smartBattery, error) {
	var raw C.battSmartBattery
	if C.battReadSmartBattery(&raw) != 0 {
		return smartBattery{}, errNoSmartBattery
	}
	return smartBattery{
		IsCharging:      raw.is_charging != 0,
		FullyCharged:    raw.fully_charged != 0,
		Voltage:         int(raw.voltage),
		Amperage:        int(raw.amperage),
		CycleCount:      int(raw.cycle_count),
		DesignCapacity:  int(raw.design_capacity),
		MaxCapacity:     int(raw.raw_max_capacity),
		SystemVoltageIn: int(raw.system_voltage_in),
		SystemCurrentIn: int(raw.system_current_in),
	}, nil
}

// batteryCapacities are battery capacities in mAh. 0 means unknown.
type batteryCapacities struct {
	Design int // DesignCapacity
	Max    int // AppleRawMaxCapacity, what the battery holds when full today
}

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
		return batteryCapacities{}, errNoSmartBattery
	default:
		return batteryCapacities{}, errors.New("AppleSmartBattery has no BatteryData dictionary")
	}
}
