package daemon

/*
#cgo LDFLAGS:  -framework CoreFoundation -framework IOKit

#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/pwr_mgt/IOPMLib.h>

// Expose the macro
const CFStringRef AssertionTypePreventSystemSleep = kIOPMAssertionTypePreventSystemSleep;
const IOPMAssertionID NullAssertionID = kIOPMNullAssertionID;
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func createAssertionSystemSleep(name, details string) (C.IOPMAssertionID, error) {
	cname := C.CString(name)
	cdetail := C.CString(details)
	defer C.free(unsafe.Pointer(cname))
	defer C.free(unsafe.Pointer(cdetail))

	cfName := C.CFStringCreateWithCString(
		C.kCFAllocatorDefault,
		cname,
		C.kCFStringEncodingUTF8,
	)
	cfDetails := C.CFStringCreateWithCString(
		C.kCFAllocatorDefault,
		cdetail,
		C.kCFStringEncodingUTF8,
	)
	defer C.CFRelease(C.CFTypeRef(cfName))
	defer C.CFRelease(C.CFTypeRef(cfDetails))

	var assertionID C.IOPMAssertionID
	status := C.IOPMAssertionCreateWithDescription(
		C.AssertionTypePreventSystemSleep,
		cfName,
		cfDetails,
		0,
		0,
		0,
		0,
		&assertionID,
	)
	if status != C.kIOReturnSuccess {
		return 0, fmt.Errorf("IOPMAssertionCreateWithDescription failed: 0x%x", uint32(status))
	}
	return assertionID, nil
}

func releaseAssertion(assertionID C.IOPMAssertionID) error {
	status := C.IOPMAssertionRelease(assertionID)
	if status != C.kIOReturnSuccess {
		return fmt.Errorf("IOPMAssertionRelease failed: 0x%x", uint32(status))
	}
	return nil
}

var assertionID C.IOPMAssertionID = C.NullAssertionID

// PreventSleepOnAC Keeps system in Dark Wake, has effect only on AC
func PreventSleepOnAC() error {
	if assertionID == C.NullAssertionID {
		id, err := createAssertionSystemSleep("batt", "Maintained charging by batt is in progress")
		if err != nil {
			return err
		}

		assertionID = id
	}
	return nil
}

// AllowSleepOnAC Releases sleep assertion, allowing sleep on AC
func AllowSleepOnAC() error {
	if assertionID != C.NullAssertionID {
		err := releaseAssertion(assertionID)
		assertionID = C.NullAssertionID
		return err
	}
	return nil
}
