package compatibility

import (
	"strconv"
	"strings"
)

// ChargeControlMode describes how charge limits are enforced on this Mac.
type ChargeControlMode string

const (
	ChargeControlUnsupported ChargeControlMode = "unsupported"
	ChargeControlLegacy      ChargeControlMode = "legacy"
	ChargeControlFirmware    ChargeControlMode = "firmware"
	// ChargeControlNative drives the manual charge limit built into macOS
	// (System Settings -> Battery -> Charge Limit) through PowerUIAgent. It is
	// used when the SMC charge-control keys are gated behind an Apple-private
	// entitlement, as on macOS 27 beta 4+ firmware.
	ChargeControlNative ChargeControlMode = "native"
	// ChargeControlAdapter holds the limit by cutting and restoring wall power
	// through the adapter SMC key, which is not entitlement-gated. batt runs
	// the hysteresis loop itself, so unlike the native limit it accepts any
	// value (including below 80%). Used on macOS 27 firmware whose charge-
	// enable keys are gated but whose adapter key still works.
	ChargeControlAdapter ChargeControlMode = "adapter"
)

// Feature identifies a daemon feature that clients may need to gate.
type Feature string

const (
	FeatureChargingControl    Feature = "charging control"
	FeatureLowerLimit         Feature = "lower charge limit"
	FeatureSleepHooks         Feature = "sleep hooks"
	FeatureMagSafeLED         Feature = "MagSafe LED control"
	FeatureAdapterControl     Feature = "power adapter control"
	FeatureAdapterSleepPolicy Feature = "adapter sleep protection"
	FeatureCalibration        Feature = "auto calibration"
)

// Capabilities reports the hardware-dependent features supported by the daemon.
type Capabilities struct {
	ChargingControl   bool              `json:"chargingControl"`
	ChargeControlMode ChargeControlMode `json:"chargeControlMode"`
	// SupportedLimits lists the only upper limits the charge-control backend
	// accepts, in ascending order. Empty means any limit from 10 to 100.
	SupportedLimits []int `json:"supportedLimits,omitempty"`
	SleepHooks      bool  `json:"sleepHooks"`
	MagSafeLED      bool  `json:"magSafeLED"`
	AdapterControl  bool  `json:"adapterControl"`
	Calibration     bool  `json:"calibration"`
}

// Permissive returns the fallback used by clients talking to an older or
// unavailable daemon. This preserves the historical behavior of exposing all
// commands when detailed compatibility data cannot be obtained.
func Permissive() Capabilities {
	return Capabilities{
		ChargingControl:   true,
		ChargeControlMode: ChargeControlLegacy,
		SleepHooks:        true,
		MagSafeLED:        true,
		AdapterControl:    true,
		Calibration:       true,
	}
}

// Supports reports whether a named feature is supported.
func (c Capabilities) Supports(feature Feature) bool {
	switch feature {
	case FeatureChargingControl:
		return c.ChargingControl
	case FeatureLowerLimit:
		// The native backend only takes an upper limit; Apple decides when
		// charging resumes. Older daemons never report the native mode, so
		// they keep the lower limit available.
		return c.ChargingControl && c.ChargeControlMode != ChargeControlNative
	case FeatureSleepHooks:
		return c.SleepHooks
	case FeatureMagSafeLED:
		return c.MagSafeLED
	case FeatureAdapterControl:
		return c.AdapterControl
	case FeatureAdapterSleepPolicy:
		return c.AdapterControl || c.ChargeControlMode == ChargeControlAdapter
	case FeatureCalibration:
		return c.Calibration
	default:
		return true
	}
}

// SupportsLimit reports whether the backend accepts the given upper limit.
// 100 (limit disabled) is always accepted.
func (c Capabilities) SupportsLimit(limit int) bool {
	if limit >= 100 || len(c.SupportedLimits) == 0 {
		return true
	}
	for _, supported := range c.SupportedLimits {
		if supported == limit {
			return true
		}
	}
	return false
}

// FormatLimits renders limits for messages, e.g. "80%, 85% or 100%".
func FormatLimits(limits []int) string {
	if len(limits) == 0 {
		return "10-100%"
	}
	parts := make([]string, 0, len(limits))
	for _, limit := range limits {
		parts = append(parts, strconv.Itoa(limit)+"%")
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
}

// NearestSupportedLimit returns the smallest supported limit that is not
// below the given one, or 100 when no supported limit is high enough. Limits
// are never rounded down so the battery is not charged beyond what the user
// configured.
func (c Capabilities) NearestSupportedLimit(limit int) int {
	if c.SupportsLimit(limit) {
		return limit
	}
	nearest := 100
	for _, supported := range c.SupportedLimits {
		if supported >= limit && supported < nearest {
			nearest = supported
		}
	}
	return nearest
}
