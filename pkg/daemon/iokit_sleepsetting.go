package daemon

/*
#cgo LDFLAGS: -framework CoreFoundation -framework IOKit

#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOReturn.h>

// These three are declared in Apple's IOKitUser sources (IOPMLibPrivate.h) but
// are not part of the public SDK. They are exported by IOKit.framework at
// runtime -- pmset(8) uses them to implement `pmset disablesleep`. Declaring
// them here mirrors what Battery Toolkit does in its IOPMPrivate module
// (BSD-3-Clause, https://github.com/mhaeuser/Battery-Toolkit).
#define kIOPMSleepDisabledKey CFSTR("SleepDisabled")
CFDictionaryRef IOPMCopySystemPowerSettings(void);
IOReturn IOPMSetSystemPowerSetting(CFStringRef key, CFTypeRef value);

// Returns 1 if sleep is disabled, 0 if it is not, -1 on failure.
static int battGetSleepDisabled(void) {
	CFDictionaryRef settings = IOPMCopySystemPowerSettings();
	if (settings == NULL) {
		return -1;
	}

	int result = 0;
	CFTypeRef value = CFDictionaryGetValue(settings, kIOPMSleepDisabledKey);
	if (value != NULL && CFGetTypeID(value) == CFBooleanGetTypeID()) {
		result = CFBooleanGetValue((CFBooleanRef)value) ? 1 : 0;
	}

	CFRelease(settings);
	return result;
}

static IOReturn battSetSleepDisabled(int disabled) {
	return IOPMSetSystemPowerSetting(
		kIOPMSleepDisabledKey,
		disabled ? kCFBooleanTrue : kCFBooleanFalse
	);
}
*/
import "C"

import "fmt"

// iokitSleepSetting is the SleepDisabled power setting, read and written
// through the private IOPM calls pmset(8) uses.
type iokitSleepSetting struct{}

func (iokitSleepSetting) Disabled() (bool, error) {
	result := C.battGetSleepDisabled()
	if result < 0 {
		return false, fmt.Errorf("IOPMCopySystemPowerSettings returned NULL")
	}
	return result == 1, nil
}

func (iokitSleepSetting) SetDisabled(disabled bool) error {
	var arg C.int
	if disabled {
		arg = 1
	}
	status := C.battSetSleepDisabled(arg)
	if status != C.kIOReturnSuccess {
		return fmt.Errorf("IOPMSetSystemPowerSetting(SleepDisabled=%t) failed: 0x%x", disabled, uint32(status))
	}
	return nil
}
