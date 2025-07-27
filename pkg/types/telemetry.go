package types

// PowerTelemetry holds the calculated power data from the SMC.
// This struct is shared between the daemon and client packages.
type PowerTelemetry struct {
	ACPower      float64 `json:"ac_power"`
	BatteryPower float64 `json:"battery_power"`
	SystemPower  float64 `json:"system_power"`
	ACVoltage    float64 `json:"ac_voltage"`
	ACAmperage   float64 `json:"ac_amperage"`
}
