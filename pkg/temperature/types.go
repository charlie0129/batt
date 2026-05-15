package temperature

type Scenario string

const (
	ScenarioIdleNotCharging Scenario = "idleNotCharging"
	ScenarioIdleCharging    Scenario = "idleCharging"
	ScenarioActiveCharging  Scenario = "activeCharging"
)

type References struct {
	IdleNotCharging *float64 `json:"idleNotCharging,omitempty"`
	IdleCharging    *float64 `json:"idleCharging,omitempty"`
	ActiveCharging  *float64 `json:"activeCharging,omitempty"`
}

type Status struct {
	MonitoringEnabled                    bool       `json:"monitoringEnabled"`
	ProtectionThresholdCelsius           int        `json:"protectionThresholdCelsius"`
	ProtectionActive                     bool       `json:"protectionActive"`
	CurrentCelsius                       *float64   `json:"currentCelsius,omitempty"`
	CurrentScenario                      Scenario   `json:"currentScenario,omitempty"`
	UserActive                           bool       `json:"userActive"`
	Charging                             bool       `json:"charging"`
	References                           References `json:"references"`
	LastUpdatedUnix                      int64      `json:"lastUpdatedUnix,omitempty"`
	LastTemperatureReferenceWriteUnix    int64      `json:"lastTemperatureReferenceWriteUnix,omitempty"`
	TemperatureUnavailableReason         string     `json:"temperatureUnavailableReason,omitempty"`
	ActivityUnavailableReason            string     `json:"activityUnavailableReason,omitempty"`
	RecoveryThresholdCelsius             int        `json:"recoveryThresholdCelsius"`
}

func (r References) Empty() bool {
	return r.IdleNotCharging == nil && r.IdleCharging == nil && r.ActiveCharging == nil
}

func (r References) Value(s Scenario) *float64 {
	switch s {
	case ScenarioIdleNotCharging:
		return r.IdleNotCharging
	case ScenarioIdleCharging:
		return r.IdleCharging
	case ScenarioActiveCharging:
		return r.ActiveCharging
	default:
		return nil
	}
}

func (r *References) Set(s Scenario, value float64) {
	if r == nil {
		return
	}

	v := value
	switch s {
	case ScenarioIdleNotCharging:
		r.IdleNotCharging = &v
	case ScenarioIdleCharging:
		r.IdleCharging = &v
	case ScenarioActiveCharging:
		r.ActiveCharging = &v
	}
}

func Label(s Scenario) string {
	switch s {
	case ScenarioIdleNotCharging:
		return "Idle + Not Charging"
	case ScenarioIdleCharging:
		return "Idle + Charging"
	case ScenarioActiveCharging:
		return "Active + Charging"
	default:
		return ""
	}
}
