package daemon

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// CalibrationPhase defines phases for auto calibration.
type CalibrationPhase string

const (
	CalPhaseIdle      CalibrationPhase = "Idle"
	CalPhaseDischarge CalibrationPhase = "DischargeToThreshold"
	CalPhaseCharge    CalibrationPhase = "ChargeToFull"
	CalPhaseHold      CalibrationPhase = "HoldAfterFull"
	CalPhasePostHold  CalibrationPhase = "DischargeAfterHold"
	CalPhaseRestore   CalibrationPhase = "RestoreAndFinish"
	CalPhaseError     CalibrationPhase = "Error"
)

// smc accessors (function vars) for test seam; default to smcConn methods.
var (
	smcGetBatteryCharge  = func() (int, error) { return smcConn.GetBatteryCharge() }
	smcIsChargingEnabled = func() (bool, error) { return smcConn.IsChargingEnabled() }
	smcEnableCharging    = func() error { return smcConn.EnableCharging() }
	smcDisableCharging   = func() error { return smcConn.DisableCharging() }
	smcIsAdapterEnabled  = func() (bool, error) { return smcConn.IsAdapterEnabled() }
	smcEnableAdapter     = func() error { return smcConn.EnableAdapter() }
	smcDisableAdapter    = func() error { return smcConn.DisableAdapter() }
	smcIsPluggedIn       = func() (bool, error) { return smcConn.IsPluggedIn() }
)

// CalibrationState holds runtime state persisted to disk.
type CalibrationState struct {
	Phase     CalibrationPhase `json:"phase"`
	StartedAt time.Time        `json:"startedAt"`
	Paused    bool             `json:"paused"`
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

var calibrationMu = &sync.Mutex{}
var calibrationState = &CalibrationState{Phase: CalPhaseIdle}
var calibrationStatePath = "" // set during daemon Run? Could derive from config path + suffix.

// smc accessors (function vars) for test seam; default to smcConn methods.
// function vars declared above

func initCalibrationState(path string) {
	calibrationStatePath = path
	// Try load existing state
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		logrus.WithError(err).Warn("failed to read calibration state")
		return
	}
	var st CalibrationState
	if err := json.Unmarshal(b, &st); err != nil {
		logrus.WithError(err).Warn("failed to unmarshal calibration state")
		return
	}
	// On restart, mark paused (safety) if mid-flow
	if st.Phase != CalPhaseIdle && st.Phase != CalPhaseRestore && st.Phase != CalPhaseError {
		st.Paused = true
	}
	calibrationState = &st
}

func persistCalibrationState() {
	if calibrationStatePath == "" {
		return
	}
	b, err := json.MarshalIndent(calibrationState, "", "  ")
	if err != nil {
		logrus.WithError(err).Error("marshal calibration state")
		return
	}
	if err := os.WriteFile(calibrationStatePath, b, 0644); err != nil {
		logrus.WithError(err).Error("write calibration state")
	}
}

func startCalibration(threshold, holdMinutes int) error {
	calibrationMu.Lock()
	defer calibrationMu.Unlock()
	if calibrationState.Phase != CalPhaseIdle && calibrationState.Phase != CalPhaseError {
		return ErrCalibrationInProgress
	}
	if threshold < 5 {
		threshold = 5
	}
	if threshold > 95 {
		threshold = 95
	}
	if holdMinutes < 10 {
		holdMinutes = 10
	}
	if holdMinutes > 24*60 {
		holdMinutes = 24 * 60
	}
	upper := conf.UpperLimit()
	lower := conf.LowerLimit()
	chargingEnabled, _ := smcIsChargingEnabled()
	adapterEnabled, _ := smcIsAdapterEnabled()

	calibrationState = &CalibrationState{
		Phase:              CalPhaseDischarge,
		StartedAt:          time.Now(),
		Paused:             false,
		SnapshotUpperLimit: upper,
		SnapshotLowerLimit: lower,
		SnapshotMaintain:   upper < 100,
		SnapshotAdapterOn:  adapterEnabled,
		SnapshotChargingOn: chargingEnabled,
		Threshold:          threshold,
		HoldMinutes:        holdMinutes,
	}
	persistCalibrationState()
	return nil
}

var ErrCalibrationInProgress = &calibrationError{"calibration already in progress"}
var ErrCalibrationNotRunning = &calibrationError{"calibration not running"}
var ErrCalibrationPaused = &calibrationError{"calibration paused"}

type calibrationError struct{ msg string }

func (e *calibrationError) Error() string { return e.msg }

// applyCalibrationWithinLoop advances calibration phases using a provided charge reading.
// Returns true if calibration is active (non-idle & non-error & not paused).
func applyCalibrationWithinLoop(charge int) bool {
	calibrationMu.Lock()
	defer calibrationMu.Unlock()
	st := calibrationState
	if st.Phase == CalPhaseIdle || st.Phase == CalPhaseError || st.Paused {
		return false
	}

	logrus.WithFields(logrus.Fields{
		"charge": charge,
		"phase":  st.Phase,
	}).Debug("calibration loop")

	switch st.Phase {
	case CalPhaseDischarge:
		if charge < st.Threshold {
			st.Phase = CalPhaseCharge
			if err := smcEnableAdapter(); err != nil {
				st.LastError = err.Error()
				st.Phase = CalPhaseError
				persistCalibrationState()
				return true
			}
			conf.SetUpperLimit(100)
			if err := conf.Save(); err != nil {
				st.LastError = err.Error()
				st.Phase = CalPhaseError
			}
		} else {
			err := smcDisableAdapter()
			if err != nil {
				logrus.WithError(err).Error("failed to disable adapter during discharge phase")

				st.LastError = err.Error()
				st.Phase = CalPhaseError
				persistCalibrationState()
				return true
			}
		}
	case CalPhaseCharge:
		if charge >= 100 {
			st.Phase = CalPhaseHold
			st.HoldEndTime = time.Now().Add(time.Duration(st.HoldMinutes) * time.Minute)
		}
	case CalPhaseHold:
		if time.Now().After(st.HoldEndTime) {
			// Begin post-hold discharge back to previous upper limit (if snapshot < 100) or current configured upper.
			st.Phase = CalPhasePostHold
			// Ensure charging disabled to allow discharge.
			_ = smcDisableAdapter()
		}
	case CalPhasePostHold:
		// Determine target (original snapshot upper limit if it was <100, else current config upper limit).
		// Using snapshotUpperLimit ensures we settle exactly back to prior maintain level before restoring limits & adapter/charging flags.
		target := st.SnapshotUpperLimit
		if target <= 0 || target > 100 { // sanity fallback
			target = conf.UpperLimit()
		}
		if charge <= target {
			st.Phase = CalPhaseRestore
		}
	case CalPhaseRestore:
		conf.SetUpperLimit(st.SnapshotUpperLimit)
		conf.SetLowerLimit(st.SnapshotLowerLimit)
		if err := conf.Save(); err != nil {
			st.LastError = err.Error()
			st.Phase = CalPhaseError
			break
		}
		if st.SnapshotChargingOn {
			_ = smcEnableCharging()
		} else {
			_ = smcDisableCharging()
		}
		if st.SnapshotAdapterOn {
			_ = smcEnableAdapter()
		} else {
			_ = smcDisableAdapter()
		}
		st.Phase = CalPhaseIdle
	}
	persistCalibrationState()
	return true
}

func pauseCalibration() error {
	calibrationMu.Lock()
	defer calibrationMu.Unlock()
	if calibrationState.Phase == CalPhaseIdle {
		return ErrCalibrationNotRunning
	}
	if !calibrationState.Paused {
		calibrationState.Paused = true
		calibrationState.PauseStartedAt = time.Now()
	}
	persistCalibrationState()
	return nil
}

func resumeCalibration() error {
	calibrationMu.Lock()
	defer calibrationMu.Unlock()
	if calibrationState.Phase == CalPhaseIdle {
		return ErrCalibrationNotRunning
	}
	if !calibrationState.Paused {
		return nil
	}
	if calibrationState.Phase == CalPhaseHold && !calibrationState.PauseStartedAt.IsZero() {
		pausedDur := time.Since(calibrationState.PauseStartedAt)
		calibrationState.HoldEndTime = calibrationState.HoldEndTime.Add(pausedDur)
	}
	calibrationState.Paused = false
	calibrationState.PauseStartedAt = time.Time{}
	persistCalibrationState()
	return nil
}

func cancelCalibration() error {
	calibrationMu.Lock()
	defer calibrationMu.Unlock()
	if calibrationState.Phase == CalPhaseIdle {
		return ErrCalibrationNotRunning
	}
	st := calibrationState
	conf.SetUpperLimit(st.SnapshotUpperLimit)
	conf.SetLowerLimit(st.SnapshotLowerLimit)
	if err := conf.Save(); err != nil {
		logrus.WithError(err).Warn("failed to save config while canceling calibration")
	}
	if st.SnapshotChargingOn {
		_ = smcEnableCharging()
	} else {
		_ = smcDisableCharging()
	}
	if st.SnapshotAdapterOn {
		_ = smcEnableAdapter()
	} else {
		_ = smcDisableAdapter()
	}
	calibrationState = &CalibrationState{Phase: CalPhaseIdle}
	persistCalibrationState()
	return nil
}

type calibrationStatus struct {
	Phase             CalibrationPhase `json:"phase"`
	ChargePercent     int              `json:"chargePercent"`
	PluggedIn         bool             `json:"pluggedIn"`
	RemainingHoldSecs int              `json:"remainingHoldSeconds"`
	StartedAt         time.Time        `json:"startedAt"`
	Paused            bool             `json:"paused"`
	CanPause          bool             `json:"canPause"`
	CanCancel         bool             `json:"canCancel"`
	Message           string           `json:"message"`
	TargetPercent     int              `json:"targetPercent,omitempty"`
}

func getCalibrationStatus() *calibrationStatus {
	calibrationMu.Lock()
	defer calibrationMu.Unlock()
	st := calibrationState
	charge, _ := smcGetBatteryCharge()
	if charge < 0 {
		charge = 0
	} else if charge > 100 {
		charge = 100
	}
	plugged, _ := smcIsPluggedIn()
	remain := 0
	if st.Phase == CalPhaseHold && !st.HoldEndTime.IsZero() {
		effectiveEnd := st.HoldEndTime
		if st.Paused && !st.PauseStartedAt.IsZero() {
			effectiveEnd = effectiveEnd.Add(time.Since(st.PauseStartedAt))
		}
		if time.Until(effectiveEnd) > 0 {
			remain = int(time.Until(effectiveEnd).Seconds())
		}
	}
	msg := st.LastError
	// Default target for PostHold is the snapshot upper limit (fallback to current conf upper if invalid)
	target := 0
	if st.Phase == CalPhasePostHold {
		if st.SnapshotUpperLimit > 0 && st.SnapshotUpperLimit <= 100 {
			target = st.SnapshotUpperLimit
		} else {
			target = conf.UpperLimit()
		}
	}
	return &calibrationStatus{
		Phase: st.Phase, ChargePercent: charge, PluggedIn: plugged,
		RemainingHoldSecs: remain, StartedAt: st.StartedAt, Paused: st.Paused,
		CanPause:      st.Phase != CalPhaseIdle && st.Phase != CalPhaseRestore && st.Phase != CalPhaseError,
		CanCancel:     st.Phase != CalPhaseIdle,
		Message:       msg,
		TargetPercent: target,
	}
}
