package temperature

type Status struct {
	MonitoringEnabled            bool     `json:"monitoringEnabled"`
	ProtectionThresholdCelsius   int      `json:"protectionThresholdCelsius"`
	ProtectionActive             bool     `json:"protectionActive"`
	CurrentCelsius               *float64 `json:"currentCelsius,omitempty"`
	Charging                     bool     `json:"charging"`
	LastUpdatedUnix              int64    `json:"lastUpdatedUnix,omitempty"`
	TemperatureUnavailableReason string   `json:"temperatureUnavailableReason,omitempty"`
	RecoveryThresholdCelsius     int      `json:"recoveryThresholdCelsius"`
}
