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
// - Design: mWh
// - ChargeRate: mW (may be negative when discharging)
// - DesignVoltage: Volts
type Battery struct {
	State         BatteryState `json:"State"`
	Design        int          `json:"Design"`
	ChargeRate    int          `json:"ChargeRate"`
	DesignVoltage float64      `json:"DesignVoltage"`
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
