package compatibility

// ChargeControlMode describes how charge limits are enforced on this Mac.
type ChargeControlMode string

const (
	ChargeControlUnsupported ChargeControlMode = "unsupported"
	ChargeControlLegacy      ChargeControlMode = "legacy"
	ChargeControlFirmware    ChargeControlMode = "firmware"
)

// Feature identifies a daemon feature that clients may need to gate.
type Feature string

const (
	FeatureChargingControl Feature = "charging control"
	FeatureSleepHooks      Feature = "sleep hooks"
	FeatureMagSafeLED      Feature = "MagSafe LED control"
	FeatureAdapterControl  Feature = "power adapter control"
	FeatureCalibration     Feature = "auto calibration"
)

// Capabilities reports the hardware-dependent features supported by the daemon.
type Capabilities struct {
	ChargingControl   bool              `json:"chargingControl"`
	ChargeControlMode ChargeControlMode `json:"chargeControlMode"`
	SleepHooks        bool              `json:"sleepHooks"`
	MagSafeLED        bool              `json:"magSafeLED"`
	AdapterControl    bool              `json:"adapterControl"`
	Calibration       bool              `json:"calibration"`
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
	case FeatureSleepHooks:
		return c.SleepHooks
	case FeatureMagSafeLED:
		return c.MagSafeLED
	case FeatureAdapterControl:
		return c.AdapterControl
	case FeatureCalibration:
		return c.Calibration
	default:
		return true
	}
}
