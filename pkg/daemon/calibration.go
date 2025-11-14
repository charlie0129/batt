package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/events"
	"github.com/sirupsen/logrus"
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

var (
	calibrationMu        = &sync.Mutex{}
	calibrationState     = &calibration.State{Phase: calibration.PhaseIdle}
	calibrationStatePath = "" // set during daemon Run? Could derive from config path + suffix.
)

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
	var st calibration.State
	if err := json.Unmarshal(b, &st); err != nil {
		logrus.WithError(err).Warn("failed to unmarshal calibration state")
		return
	}
	// On restart, mark paused (safety) if mid-flow
	if st.Phase != calibration.PhaseIdle && st.Phase != calibration.PhaseRestore && st.Phase != calibration.PhaseError {
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

	if calibrationState.Phase != calibration.PhaseIdle && calibrationState.Phase != calibration.PhaseError {
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

	if sseHub != nil {
		sseHub.Publish(events.CalibrationPhase, events.CalibrationPhaseEvent{
			From:    string(calibrationState.Phase),
			To:      string(calibration.PhaseDischarge),
			Message: fmt.Sprintf("Start calibration: discharging to %d%%", threshold),
			Ts:      time.Now().Unix(),
		})

		logrus.WithField("event", events.CalibrationPhase).Debug("new event")
	}

	calibrationState = &calibration.State{
		Phase:              calibration.PhaseDischarge,
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
	prevPhase := st.Phase
	if st.Phase == calibration.PhaseIdle || st.Phase == calibration.PhaseError || st.Paused {
		return false
	}

	logrus.WithFields(logrus.Fields{
		"charge": charge,
		"phase":  st.Phase,
	}).Debug("calibration loop")

	switch st.Phase {
	case calibration.PhaseDischarge:
		if charge < st.Threshold {
			st.Phase = calibration.PhaseCharge
			if err := smcEnableAdapter(); err != nil {
				st.LastError = err.Error()
				st.Phase = calibration.PhaseError
				break
			}
			conf.SetUpperLimit(100)
			if err := conf.Save(); err != nil {
				st.LastError = err.Error()
				st.Phase = calibration.PhaseError
			}
		} else {
			enabled, err := smcIsAdapterEnabled()
			if err != nil {
				logrus.WithError(err).Error("failed to check adapter state during discharge phase")

				st.LastError = err.Error()
				st.Phase = calibration.PhaseError
				break
			}

			if enabled {
				err := smcDisableAdapter()
				if err != nil {
					logrus.WithError(err).Error("failed to disable adapter during discharge phase")

					st.LastError = err.Error()
					st.Phase = calibration.PhaseError
				}
			}
		}
	case calibration.PhaseCharge:
		if charge >= 100 {
			st.Phase = calibration.PhaseHold
			st.HoldEndTime = time.Now().Add(time.Duration(st.HoldMinutes) * time.Minute)
		}
	case calibration.PhaseHold:
		if time.Now().After(st.HoldEndTime) {
			// Begin post-hold discharge back to previous upper limit (if snapshot < 100) or current configured upper.
			st.Phase = calibration.PhasePostHold
			// Ensure charging disabled to allow discharge.
			_ = smcDisableAdapter()
		}
	case calibration.PhasePostHold:
		// Determine target (original snapshot upper limit if it was <100, else current config upper limit).
		// Using snapshotUpperLimit ensures we settle exactly back to prior maintain level before restoring limits & adapter/charging flags.
		target := st.SnapshotUpperLimit
		if target <= 50 || target > 100 { // sanity fallback
			target = conf.UpperLimit()
		}
		if charge <= target {
			st.Phase = calibration.PhaseRestore
		}
	case calibration.PhaseRestore:
		conf.SetUpperLimit(st.SnapshotUpperLimit)
		conf.SetLowerLimit(st.SnapshotLowerLimit)
		if err := conf.Save(); err != nil {
			st.LastError = err.Error()
			st.Phase = calibration.PhaseError
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
		st.Phase = calibration.PhaseIdle
	}
	persistCalibrationState()

	// Broadcast phase change if any
	if sseHub != nil && st.Phase != prevPhase {
		sseHub.Publish(events.CalibrationPhase, events.CalibrationPhaseEvent{
			From: string(prevPhase),
			To:   string(st.Phase),
			Message: func() string {
				if st.Phase == calibration.PhaseError {
					return st.LastError
				}
				switch st.Phase {
				case calibration.PhaseCharge:
					return "Start charging to full"
				case calibration.PhaseHold:
					return fmt.Sprintf("Holding at full charge for %d minutes (end of %s)", st.HoldMinutes, st.HoldEndTime.Local().Format("03:04 PM"))
				case calibration.PhasePostHold:
					return fmt.Sprintf("Discharging to restore limits to %d%%", st.SnapshotUpperLimit)
				case calibration.PhaseRestore:
					return fmt.Sprintf("Calibration completed in %s", formatDuration(time.Since(st.StartedAt)))
				case calibration.PhaseError:
					return st.LastError
				}
				return ""
			}(),
			Ts: time.Now().Unix(),
		})

		logrus.WithField("event", events.CalibrationPhase).Debug("new event")
	}
	return true
}

func pauseCalibration() error {
	calibrationMu.Lock()
	defer calibrationMu.Unlock()
	if calibrationState.Phase == calibration.PhaseIdle {
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
	if calibrationState.Phase == calibration.PhaseIdle {
		return ErrCalibrationNotRunning
	}
	if !calibrationState.Paused {
		return nil
	}
	if calibrationState.Phase == calibration.PhaseHold && !calibrationState.PauseStartedAt.IsZero() {
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
	if calibrationState.Phase == calibration.PhaseIdle {
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
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	persistCalibrationState()
	return nil
}

func getCalibrationStatus() *calibration.Status {
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
	if st.Phase == calibration.PhaseHold && !st.HoldEndTime.IsZero() {
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
	if st.Phase == calibration.PhasePostHold {
		if st.SnapshotUpperLimit > 0 && st.SnapshotUpperLimit <= 100 {
			target = st.SnapshotUpperLimit
		} else {
			target = conf.UpperLimit()
		}
	}
	return &calibration.Status{
		Phase: st.Phase, ChargePercent: charge, PluggedIn: plugged,
		RemainingHoldSecs: remain, StartedAt: st.StartedAt, Paused: st.Paused,
		CanPause:      st.Phase != calibration.PhaseIdle && st.Phase != calibration.PhaseRestore && st.Phase != calibration.PhaseError,
		CanCancel:     st.Phase != calibration.PhaseIdle,
		Message:       msg,
		TargetPercent: target,
	}
}
