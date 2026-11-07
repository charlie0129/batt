package powerui

/*
#cgo LDFLAGS: -framework CoreFoundation -framework IOKit
#include <stdlib.h>
#include "assertions.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

// SleepAssertion is a power assertion that a powerd restart would drop.
type SleepAssertion struct {
	PID  int
	Type string
	Name string
}

// ForeignSleepAssertions returns sleep and display-sleep assertions held by
// processes other than powerd, including batt's own calibration assertions.
// The name is retained for the daemon's existing interface; batt assertions
// are deliberately included so a restart cannot discard them.
func ForeignSleepAssertions() ([]SleepAssertion, error) {
	var list *C.battSleepAssertion
	var count C.int
	var cerr *C.char
	if C.battCopySleepAssertions(&list, &count, &cerr) != 0 { //nolint:gocritic // cgo output parameters
		if cerr != nil {
			defer C.free(unsafe.Pointer(cerr))
			return nil, errors.New(C.GoString(cerr))
		}
		return nil, errors.New("could not read system power assertions")
	}
	defer C.battFreeSleepAssertions(list)
	if count == 0 {
		return nil, nil
	}
	items := unsafe.Slice(list, int(count))
	result := make([]SleepAssertion, 0, len(items))
	for _, item := range items {
		result = append(result, SleepAssertion{
			PID: int(item.pid), Type: C.GoString(&item.assertion_type[0]), Name: C.GoString(&item.name[0]),
		})
	}
	return result, nil
}
