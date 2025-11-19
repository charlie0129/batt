package calibration

import "time"

// Phase defines phases for auto calibration.
type Phase string

const (
	PhaseIdle      Phase = "Idle"
	PhaseDischarge Phase = "DischargeToThreshold"
	PhaseCharge    Phase = "ChargeToFull"
	PhaseHold      Phase = "HoldAfterFull"
	PhasePostHold  Phase = "DischargeAfterHold"
	PhaseRestore   Phase = "RestoreAndFinish"
	PhaseError     Phase = "Error"
)

// Action defines user actions for auto calibration.
type Action string

const (
	ActionStart           Action = "Start"
	ActionPause           Action = "Pause"
	ActionResume          Action = "Resume"
	ActionCancel          Action = "Cancel"
	ActionSchedule        Action = "Schedule"
	ActionDisableSchedule Action = "DisableSchedule"
)

// State holds runtime state persisted to disk.
type State struct {
	Phase     Phase     `json:"phase"`
	StartedAt time.Time `json:"startedAt"`
	Paused    bool      `json:"paused"`
	// When paused, record the timestamp to properly adjust timers (e.g., Hold)
	PauseStartedAt     time.Time `json:"pauseStartedAt"`
	SnapshotUpperLimit int       `json:"snapshotUpperLimit"`
	SnapshotLowerLimit int       `json:"snapshotLowerLimit"`
	SnapshotMaintain   bool      `json:"snapshotMaintain"` // upper<100
	SnapshotAdapterOn  bool      `json:"snapshotAdapterOn"`
	SnapshotChargingOn bool      `json:"snapshotChargingOn"`
	HoldEndTime        time.Time `json:"holdEndTime"`
	Threshold          int       `json:"threshold"`
	HoldMinutes        int       `json:"holdMinutes"`
	LastError          string    `json:"lastError"`
}

// Status is a synthesized view model exposed via HTTP telemetry and GUI polling.
// It derives from persistent State plus live readings (charge %, plugged-in) and
// dynamic timers (remaining hold seconds). TargetPercent is only populated
// during PhasePostHold to indicate the discharge goal before restore.
type Status struct {
	Phase             Phase     `json:"phase"`
	ChargePercent     int       `json:"chargePercent"`
	PluggedIn         bool      `json:"pluggedIn"`
	RemainingHoldSecs int       `json:"remainingHoldSeconds"`
	StartedAt         time.Time `json:"startedAt"`
	Paused            bool      `json:"paused"`
	CanPause          bool      `json:"canPause"`
	CanCancel         bool      `json:"canCancel"`
	Message           string    `json:"message"`
	TargetPercent     int       `json:"targetPercent,omitempty"`
}
