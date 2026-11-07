package powerui

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
#include "powerui.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/charlie0129/batt/pkg/logging"
)

type nativeBridge struct{}

// FullChargeOverride reads powerd's current override, including a flag that
// remains set after charging finishes. It never changes the charging policy.
func FullChargeOverride() (bool, error) {
	var active C.int
	var cerr *C.char
	if C.batt_powerui_full_charge_override(&active, &cerr) != 0 { //nolint:gocritic
		return false, takeError(cerr)
	}
	return active == 1, nil
}

func takeError(cerr *C.char) error {
	if cerr == nil {
		return errors.New("unknown PowerUI error")
	}
	defer C.free(unsafe.Pointer(cerr))
	return errors.New(C.GoString(cerr))
}

func (b nativeBridge) Supported() bool {
	supported := C.batt_powerui_supported() == 1
	logging.Trace("probed PowerUI manual charge limit", "supported", supported)
	return supported
}

func (b nativeBridge) AvailableLimits() ([]int, error) {
	const maxLimits = 100
	var (
		raw   [maxLimits]C.int
		count C.int
		cerr  *C.char
	)
	if C.batt_powerui_available_limits(&raw[0], maxLimits, &count, &cerr) != 0 { //nolint:gocritic // dupSubExpr false positive on cgo call
		return nil, fmt.Errorf("get available charge limits: %w", takeError(cerr))
	}
	limits := make([]int, 0, int(count))
	for i := 0; i < int(count); i++ {
		limits = append(limits, int(raw[i]))
	}
	return limits, nil
}

func (b nativeBridge) Limit() (int, bool, error) {
	var (
		limit, enabled C.int
		cerr           *C.char
	)
	if C.batt_powerui_get_limit(&limit, &enabled, &cerr) != 0 { //nolint:gocritic // dupSubExpr false positive on cgo call
		return 0, false, fmt.Errorf("get native charge limit: %w", takeError(cerr))
	}
	logging.Trace("read native charge limit", "limit", int(limit), "enabled", enabled == 1)
	return int(limit), enabled == 1, nil
}

func (b nativeBridge) SetSliderLimit(limit int) error {
	if limit < 0 || limit > 100 {
		return fmt.Errorf("invalid native charge limit %d", limit)
	}

	var cerr *C.char
	if C.batt_powerui_set_limit(C.int(limit), &cerr) != 0 { //nolint:gocritic // dupSubExpr false positive on cgo call
		return fmt.Errorf("set native charge limit to %d%%: %w", limit, takeError(cerr))
	}
	logging.Trace("set native charge limit", "limit", limit)
	return nil
}

func (b nativeBridge) Disable() error {
	var cerr *C.char
	if C.batt_powerui_disable(&cerr) != 0 { //nolint:gocritic // dupSubExpr false positive on cgo call
		return fmt.Errorf("disable native charge limit: %w", takeError(cerr))
	}
	logging.Trace("disabled native charge limit")
	return nil
}

func (b nativeBridge) CustomLimits() bool {
	return C.batt_powerui_custom_limits() == 1
}

func (b nativeBridge) Preferences() (int, bool, error) {
	var limit, enabled C.int
	var cerr *C.char
	if C.batt_powerui_get_preferences(&limit, &enabled, &cerr) != 0 { //nolint:gocritic
		return 0, false, takeError(cerr)
	}
	return int(limit), enabled == 1, nil
}

func (b nativeBridge) Enable() error {
	var cerr *C.char
	if C.batt_powerui_enable(&cerr) != 0 { //nolint:gocritic
		return takeError(cerr)
	}
	return nil
}

func (b nativeBridge) WritePreferences(limit int) error {
	var cerr *C.char
	if C.batt_powerui_write_preferences(C.int(limit), &cerr) != 0 { //nolint:gocritic
		return takeError(cerr)
	}
	return nil
}

func (b nativeBridge) Policy() (int, bool, error) {
	var limit, enabled C.int
	var cerr *C.char
	if C.batt_powerui_get_policy(&limit, &enabled, &cerr) != 0 { //nolint:gocritic
		return 100, false, takeError(cerr)
	}
	return int(limit), enabled == 1, nil
}

func (b nativeBridge) SetPolicy(limit int) error {
	var cerr *C.char
	if C.batt_powerui_set_policy(C.int(limit), &cerr) != 0 { //nolint:gocritic
		return takeError(cerr)
	}
	return nil
}
