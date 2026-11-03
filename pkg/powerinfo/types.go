package powerinfo

// BatteryState represents the charging state of the battery.
type BatteryState int

const (
	// Discharging indicates the battery is discharging.
	Discharging BatteryState = iota
	// Charging indicates the battery is charging.
	Charging
	// Full indicates the battery is full.
	Full
)

// Battery is a minimal, backwards-compatible battery info structure
// containing the fields used by the batt client and CLI.
// Units:
// - Design: mAh
// - ChargeRate: mW (may be negative when discharging)
// - DesignVoltage: Volts
type Battery struct {
	State          BatteryState `json:"State"`
	DesignCapacity int          `json:"DesignCapacity"`
	MaxCapacity    int          `json:"MaxCapacity"`
	ChargeRate     int          `json:"ChargeRate"`
	DesignVoltage  float64      `json:"DesignVoltage"`

	// Nil when unavailable, including responses from older daemons.
	ChargingToFullOverride *bool `json:"ChargingToFullOverride,omitempty"`
}

const FullChargeOverrideNotice = "macOS full-charge override active; charge limit paused to allow charging to 100%."
const FullChargeOverrideAtFullNotice = "Battery full; charge limit paused until macOS clears the override."

func (b *Battery) FullChargeOverrideActive() bool {
	return b.ChargingToFullOverride != nil && *b.ChargingToFullOverride
}

func (b *Battery) FullChargeOverrideAtFull(currentCharge int) bool {
	return b.FullChargeOverrideActive() && (b.State == Full || currentCharge >= 100)
}

// PowerTelemetry holds a simplified snapshot used by the GUI.
// Values are derived from IOKit data via powerkit-go.
type PowerTelemetry struct {
	Adapter struct {
		InputVoltage  float64 `json:"InputVoltage"`
		InputAmperage float64 `json:"InputAmperage"`
	} `json:"Adapter"`
	Battery struct {
		CycleCount int `json:"CycleCount"`
	} `json:"Battery"`
	Calculations struct {
		ACPower             float64 `json:"ACPower"`
		BatteryPower        float64 `json:"BatteryPower"`
		SystemPower         float64 `json:"SystemPower"`
		HealthByMaxCapacity int     `json:"HealthByMaxCapacity"`
	} `json:"Calculations"`
}
