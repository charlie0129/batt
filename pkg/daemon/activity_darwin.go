package daemon

// #cgo LDFLAGS: -framework IOKit -framework CoreFoundation
// #include <stdint.h>
// #include <CoreFoundation/CoreFoundation.h>
// #include <IOKit/IOKitLib.h>
// static int64_t batt_hidIdleNanos(void) {
//     io_service_t service = IOServiceGetMatchingService(kIOMasterPortDefault, IOServiceMatching("IOHIDSystem"));
//     if (!service) {
//         return -1;
//     }
//     CFTypeRef property = IORegistryEntryCreateCFProperty(service, CFSTR("HIDIdleTime"), kCFAllocatorDefault, 0);
//     IOObjectRelease(service);
//     if (!property) {
//         return -1;
//     }
//     int64_t value = -1;
//     if (CFGetTypeID(property) == CFNumberGetTypeID()) {
//         CFNumberGetValue((CFNumberRef)property, kCFNumberSInt64Type, &value);
//     }
//     CFRelease(property);
//     return value;
// }
import "C"

import (
	"fmt"
	"time"
)

func userIsActive(activeWithin time.Duration) (bool, error) {
	idleNanos := int64(C.batt_hidIdleNanos())
	if idleNanos < 0 {
		return false, fmt.Errorf("HID idle time unavailable")
	}
	return time.Duration(idleNanos) < activeWithin, nil
}
