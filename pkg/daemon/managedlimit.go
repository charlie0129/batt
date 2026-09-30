package daemon

import (
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/powerui"
	"github.com/charlie0129/batt/pkg/smc"
)

// nativeLimit drives the charge limit built into macOS when the SMC keys are
// unusable. Tests replace it with a fake.
var nativeLimit powerui.Controller = powerui.New()

// isManagedChargeControl reports whether Apple, not batt, decides when to
// charge. The firmware and native backends only need the configured limit to
// be kept in sync; they never toggle charging themselves.
func isManagedChargeControl() bool {
	switch getCapabilities().ChargeControlMode {
	case compatibility.ChargeControlFirmware, compatibility.ChargeControlNative:
		return true
	default:
		return false
	}
}

// ensureManagedChargeLimit applies the configured limits to the active
// Apple-managed backend and reports whether anything had to be written.
func ensureManagedChargeLimit(lower, upper int) (bool, error) {
	switch getCapabilities().ChargeControlMode {
	case compatibility.ChargeControlFirmware:
		return smcConn.EnsureFirmwareChargeLimit(lower, upper)
	case compatibility.ChargeControlNative:
		return ensureNativeChargeLimit(upper)
	default:
		return false, smc.ErrNoChargingCapability
	}
}

// ensureManagedChargeLimitDisabled turns the Apple-managed limit off and
// reports whether it was on.
func ensureManagedChargeLimitDisabled() (bool, error) {
	switch getCapabilities().ChargeControlMode {
	case compatibility.ChargeControlFirmware:
		return smcConn.EnsureFirmwareChargeLimitDisabled()
	case compatibility.ChargeControlNative:
		return ensureNativeChargeLimitDisabled()
	default:
		return false, smc.ErrNoChargingCapability
	}
}

// ensureNativeChargeLimit keeps the macOS charge limit at the configured
// upper limit. macOS only offers a fixed set of limits; the daemon persists a
// supported value at startup, so this is only a safety net that never lowers
// the limit below what the user configured.
func ensureNativeChargeLimit(upper int) (bool, error) {
	caps := getCapabilities()
	if !caps.SupportsLimit(upper) {
		snapped := caps.NearestSupportedLimit(upper)
		logrus.WithFields(logrus.Fields{"configured": upper, "limit": snapped}).Debug("charge limit is not offered by macOS, using the next supported limit")
		upper = snapped
	}
	if upper >= 100 {
		return ensureNativeChargeLimitDisabled()
	}

	current, enabled, err := nativeLimit.Limit()
	if err != nil {
		return false, err
	}
	if enabled && current == upper {
		return false, nil
	}
	if err := nativeLimit.SetLimit(upper); err != nil {
		return false, err
	}
	return true, nil
}

func ensureNativeChargeLimitDisabled() (bool, error) {
	_, enabled, err := nativeLimit.Limit()
	if err != nil {
		return false, err
	}
	if !enabled {
		return false, nil
	}
	return true, nativeLimit.Disable()
}

// resetChargeControl restores the platform's default charging behavior. It is
// used when the daemon exits.
func resetChargeControl() error {
	switch getCapabilities().ChargeControlMode {
	case compatibility.ChargeControlNative:
		_, err := ensureNativeChargeLimitDisabled()
		return err
	case compatibility.ChargeControlAdapter:
		// Restore wall power so the Mac is not left running from the battery.
		return smcEnableAdapter()
	default:
		return smcConn.ResetChargeControl()
	}
}
