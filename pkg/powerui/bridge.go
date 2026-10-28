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

	"github.com/sirupsen/logrus"
)

type nativeBridge struct{}

func takeError(cerr *C.char) error {
	if cerr == nil {
		return errors.New("unknown PowerUI error")
	}
	defer C.free(unsafe.Pointer(cerr))
	return errors.New(C.GoString(cerr))
}

func (b nativeBridge) Supported() bool {
	supported := C.batt_powerui_supported() == 1
	logrus.WithField("supported", supported).Trace("probed PowerUI manual charge limit")
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
	logrus.WithFields(logrus.Fields{"limit": int(limit), "enabled": enabled == 1}).Trace("read native charge limit")
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
	logrus.WithField("limit", limit).Trace("set native charge limit")
	return nil
}

func (b nativeBridge) Disable() error {
	var cerr *C.char
	if C.batt_powerui_disable(&cerr) != 0 { //nolint:gocritic // dupSubExpr false positive on cgo call
		return fmt.Errorf("disable native charge limit: %w", takeError(cerr))
	}
	logrus.Trace("disabled native charge limit")
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
