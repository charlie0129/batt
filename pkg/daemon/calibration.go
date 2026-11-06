package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/events"
)

func (d *Daemon) calibrationSessionActive() bool {
	return d.calibrationState.Phase != calibration.PhaseIdle && d.calibrationState.Phase != calibration.PhaseError
}

// calibrationOwnsChargeLimit reports whether a calibration still has to write
// back the charge limit it snapshotted. It is broader than
// calibrationSessionActive: a failed calibration keeps its snapshot until it is
// cancelled, and cancelling restores it.
func (d *Daemon) calibrationOwnsChargeLimit() bool {
	d.calibrationMu.Lock()
	defer d.calibrationMu.Unlock()

	return d.calibrationState.Phase != calibration.PhaseIdle
}

func (d *Daemon) releaseCalibrationSleepAssertion() {
	if err := d.assertions.AllowCalibrationSleep(); err != nil {
		logrus.WithError(err).Error("failed to release calibration sleep assertion")
	}
}

func (d *Daemon) restoreCalibrationSleepAssertion() {
	d.calibrationMu.Lock()
	defer d.calibrationMu.Unlock()
	if !d.calibrationSessionActive() {
		return
	}
	if err := d.assertions.PreventCalibrationSleep(); err != nil {
		logrus.WithError(err).Error("failed to restore calibration sleep assertion")
	}
}

func (d *Daemon) calibrationNeedsMaintainLoop() bool {
	d.calibrationMu.Lock()
	defer d.calibrationMu.Unlock()
	return d.calibrationSessionActive() && !d.calibrationState.Paused
}

func (d *Daemon) enableChargingForCalibration() error {
	if d.isManagedChargeControl() {
		_, err := d.ensureManagedChargeLimitDisabled()
		return err
	}
	return d.smc.EnableCharging()
}

func (d *Daemon) restoreChargeControlAfterCalibration(st *calibration.State) {
	if d.isManagedChargeControl() {
		var err error
		if st.SnapshotMaintain {
			_, err = d.ensureManagedChargeLimit(st.SnapshotLowerLimit, st.SnapshotUpperLimit)
		} else {
			_, err = d.ensureManagedChargeLimitDisabled()
		}
		if err != nil {
			logrus.WithError(err).Errorf("failed to restore %s charge limit after calibration", d.getCapabilities().ChargeControlMode)
		}
		return
	}

	if st.SnapshotChargingOn {
		_ = d.smc.EnableCharging()
	} else {
		_ = d.smc.DisableCharging()
	}
}

// loadCalibrationState reads the persisted calibration state from path and
// keeps persisting it there. A calibration that was mid-flow is restored
// paused, for safety.
func (d *Daemon) loadCalibrationState(path string) {
	d.calibrationStatePath = path
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
	d.calibrationState = &st
}

func (d *Daemon) persistCalibrationState() {
	if d.calibrationStatePath == "" {
		return
	}
	b, err := json.MarshalIndent(d.calibrationState, "", "  ")
	if err != nil {
		logrus.WithError(err).Error("marshal calibration state")
		return
	}
	if err := os.WriteFile(d.calibrationStatePath, b, 0644); err != nil {
		logrus.WithError(err).Error("write calibration state")
	}
}

func (d *Daemon) startCalibration(threshold, holdMinutes int) error {
	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	d.calibrationMu.Lock()
	defer d.calibrationMu.Unlock()

	conf := d.conf
	if d.calibrationState.Phase == calibration.PhaseError {
		// A failed calibration may have left the charge limit at 100. Its snapshot
		// is the only record of the original limit, and Cancel restores it.
		return ErrCalibrationControlsChargeLimit
	}
	if d.calibrationState.Phase != calibration.PhaseIdle {
		return ErrCalibrationInProgress
	}
	if !conf.DisableUntil().IsZero() {
		return ErrTemporaryDisableInProgress
	}
	if !conf.AdapterDisableUntil().IsZero() {
		return ErrTemporaryAdapterDisableInProgress
	}
	if conf.ChargeOnceTarget() != 0 {
		return ErrChargeOnceInProgress
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
	chargingEnabled := true
	if !d.isManagedChargeControl() {
		var err error
		chargingEnabled, err = d.smc.IsChargingEnabled()
		if err != nil {
			return fmt.Errorf("failed to check charging state before starting calibration: %w", err)
		}
	}
	adapterEnabled, err := d.smc.IsAdapterEnabled()
	if err != nil {
		return fmt.Errorf("failed to check adapter state before starting calibration: %w", err)
	}

	if d.getCapabilities().AdapterControl {
		if err := d.reconcileAdapterSleepPolicy(); err != nil {
			return fmt.Errorf("failed to reconcile adapter sleep policy before starting calibration: %w", err)
		}
	}
	if err := d.assertions.PreventCalibrationSleep(); err != nil {
		return fmt.Errorf("prevent sleep during calibration: %w", err)
	}

	d.hub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
		Action:  string(calibration.ActionStart),
		Message: fmt.Sprintf("Start calibration: discharging to %d%%", threshold),
		Ts:      time.Now().Unix(),
	})

	d.calibrationState = &calibration.State{
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

	d.persistCalibrationState()

	return nil
}

var ErrCalibrationInProgress = &calibrationError{"calibration already in progress"}
var ErrCalibrationNotRunning = &calibrationError{"calibration not running"}
var ErrCalibrationPaused = &calibrationError{"calibration paused"}
var ErrCalibrationControlsChargeLimit = &calibrationError{"a calibration is in progress or awaiting cancellation, which controls the charge limit itself. Cancel it first with 'batt calibration cancel'"}
var ErrTemporaryDisableInProgress = &calibrationError{"a temporary charge-limit disable is in progress; wait for it to finish or set a charge limit to cancel it"}
var ErrCalibrationControlsAdapter = &calibrationError{"a calibration is in progress or awaiting cancellation, which controls the power adapter itself. Cancel it first with 'batt calibration cancel'"}
var ErrTemporaryAdapterDisableInProgress = &calibrationError{"a temporary power-adapter disable is in progress; wait for it to finish or run 'batt adapter enable' to cancel it"}

type calibrationError struct{ msg string }

func (e *calibrationError) Error() string { return e.msg }

type calibrationStatus struct {
	charge int
	phase  calibration.Phase
}

// applyCalibrationWithinLoop advances calibration phases using a provided charge reading.
// Returns true if calibration is active (non-idle & non-error & not paused).
//
//nolint:gocyclo
func (d *Daemon) applyCalibrationWithinLoop(charge int) bool {
	d.calibrationMu.Lock()
	defer d.calibrationMu.Unlock()
	conf := d.conf
	st := d.calibrationState
	prevPhase := st.Phase
	if st.Phase == calibration.PhaseIdle || st.Phase == calibration.PhaseError || st.Paused {
		return false
	}

	log := logrus.WithFields(logrus.Fields{
		"phase":     st.Phase,
		"charge":    charge,
		"operation": "calibration",
	})
	// Throttle debug logs to changes only.
	if d.lastCalibrationStatus.charge != charge || d.lastCalibrationStatus.phase != st.Phase {
		d.lastCalibrationStatus.charge = charge
		d.lastCalibrationStatus.phase = st.Phase
		log.Debug("calibration loop")
	}

	switch st.Phase {
	case calibration.PhaseDischarge:
		if charge < st.Threshold {
			// Transition to charge phase.
			st.Phase = calibration.PhaseCharge
			logrus.Info("discharge phase complete. starting charge phase")
			logrus.Info("enabling adapter")
			if err := d.enableAdapter(); err != nil {
				st.LastError = err.Error()
				st.Phase = calibration.PhaseError
				break
			}
			logrus.Info("enabling charging")
			if err := d.enableChargingForCalibration(); err != nil {
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
			log.Info("disabling adapter to allow discharge")
			if err := d.disableAdapter(); err != nil {
				logrus.WithError(err).Error("failed to disable adapter during discharge phase")
				st.LastError = err.Error()
				st.Phase = calibration.PhaseError
			}
		}
	case calibration.PhaseCharge:
		if charge >= 100 {
			logrus.WithField("holdDuration", time.Duration(st.HoldMinutes)*time.Minute).Info("charge phase complete. starting hold phase")
			st.Phase = calibration.PhaseHold
			st.HoldEndTime = time.Now().Add(time.Duration(st.HoldMinutes) * time.Minute)
		}
	case calibration.PhaseHold:
		if time.Now().After(st.HoldEndTime) {
			logrus.Info("hold phase complete. starting post-hold phase, draining to previous limits")
			// Begin post-hold discharge back to previous upper limit (if snapshot < 100) or current configured upper.
			// Ensure charging disabled to allow discharge. The post-hold phase never cuts the adapter
			// again, so a failure here must end the calibration instead of waiting at full charge.
			if err := d.disableAdapter(); err != nil {
				logrus.WithError(err).Error("failed to disable adapter during hold phase")
				st.LastError = err.Error()
				// The limit is still 100 here. Write the snapshot back, as Cancel does, so the
				// failed run does not leave the battery without a limit while it waits for
				// Cancel. The maintain loop drops a calibration in the error phase when the
				// limit is 100, without restoring anything.
				conf.SetUpperLimit(st.SnapshotUpperLimit)
				conf.SetLowerLimit(st.SnapshotLowerLimit)
				if saveErr := conf.Save(); saveErr != nil {
					logrus.WithError(saveErr).Warn("failed to save config after the calibration failed")
				}
				d.restoreChargeControlAfterCalibration(st)
				st.Phase = calibration.PhaseError
				break
			}
			st.Phase = calibration.PhasePostHold
		}
	case calibration.PhasePostHold:
		// Determine target (original snapshot upper limit if it was <100, else current config upper limit).
		// Using snapshotUpperLimit ensures we settle exactly back to prior maintain level before restoring limits & adapter/charging flags.
		target := st.SnapshotUpperLimit
		if target <= 20 || target > 100 { // sanity fallback
			target = conf.UpperLimit()
		}
		if charge <= target {
			logrus.Info("post-hold phase complete. starting restore phase")
			st.Phase = calibration.PhaseRestore
		}
	case calibration.PhaseRestore:
		logrus.WithFields(logrus.Fields{
			"upperLimit":       st.SnapshotUpperLimit,
			"lowerLimit":       st.SnapshotLowerLimit,
			"isCharging":       st.SnapshotChargingOn,
			"isAdapterEnabled": st.SnapshotAdapterOn,
		}).Info("restoring previous battery config and finishing calibration")
		conf.SetUpperLimit(st.SnapshotUpperLimit)
		conf.SetLowerLimit(st.SnapshotLowerLimit)
		if err := conf.Save(); err != nil {
			st.LastError = err.Error()
			st.Phase = calibration.PhaseError
			break
		}
		d.restoreChargeControlAfterCalibration(st)
		var adapterErr error
		if st.SnapshotAdapterOn {
			adapterErr = d.enableAdapter()
		} else {
			adapterErr = d.disableAdapter()
		}
		if adapterErr != nil {
			logrus.WithError(adapterErr).Error("failed to restore adapter during calibration restore phase")
			st.LastError = adapterErr.Error()
			st.Phase = calibration.PhaseError
			break
		}
		st.Phase = calibration.PhaseIdle
	}
	d.persistCalibrationState()
	if st.Phase == calibration.PhaseIdle || st.Phase == calibration.PhaseError {
		d.releaseCalibrationSleepAssertion()
	}

	// Broadcast phase change if any
	if st.Phase != prevPhase {
		d.hub.Publish(events.CalibrationPhase, events.CalibrationPhaseEvent{
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
					return fmt.Sprintf("Holding at full charge for %d minutes (ends %s)", st.HoldMinutes, st.HoldEndTime.Local().Format("03:04 PM"))
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

func (d *Daemon) pauseCalibration() error {
	d.calibrationMu.Lock()
	defer d.calibrationMu.Unlock()
	st := d.calibrationState
	if st.Phase == calibration.PhaseIdle {
		return ErrCalibrationNotRunning
	}
	if !st.Paused {
		st.Paused = true
		st.PauseStartedAt = time.Now()

		d.hub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
			Action:  string(calibration.ActionPause),
			Message: fmt.Sprintf("Calibration paused at phase %s", st.Phase),
			Ts:      time.Now().Unix(),
		})

		d.persistCalibrationState()
	}

	return nil
}

func (d *Daemon) resumeCalibration() error {
	d.calibrationMu.Lock()
	defer d.calibrationMu.Unlock()
	st := d.calibrationState
	if st.Phase == calibration.PhaseIdle {
		return ErrCalibrationNotRunning
	}
	if !st.Paused {
		return nil
	}
	if st.Phase == calibration.PhaseHold && !st.PauseStartedAt.IsZero() {
		pausedDur := time.Since(st.PauseStartedAt)
		st.HoldEndTime = st.HoldEndTime.Add(pausedDur)
	}

	d.hub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
		Action:  string(calibration.ActionResume),
		Message: fmt.Sprintf("Calibration resumed (paused at %s)", st.PauseStartedAt.Format("Jan _2 15:04")),
		Ts:      time.Now().Unix(),
	})

	st.Paused = false
	st.PauseStartedAt = time.Time{}

	d.persistCalibrationState()
	return nil
}

func (d *Daemon) cancelCalibration() error {
	d.calibrationMu.Lock()
	defer d.calibrationMu.Unlock()
	if d.calibrationState.Phase == calibration.PhaseIdle {
		return ErrCalibrationNotRunning
	}

	conf := d.conf
	st := d.calibrationState
	conf.SetUpperLimit(st.SnapshotUpperLimit)
	conf.SetLowerLimit(st.SnapshotLowerLimit)
	if err := conf.Save(); err != nil {
		logrus.WithError(err).Warn("failed to save config while canceling calibration")
	}

	d.restoreChargeControlAfterCalibration(st)
	var adapterErr error
	if st.SnapshotAdapterOn {
		adapterErr = d.enableAdapter()
	} else {
		adapterErr = d.disableAdapter()
	}
	if adapterErr != nil {
		logrus.WithError(adapterErr).Error("failed to restore adapter while canceling calibration")
		st.LastError = adapterErr.Error()
		st.Phase = calibration.PhaseError
		d.persistCalibrationState()
		return fmt.Errorf("failed to restore adapter while canceling calibration: %w", adapterErr)
	}

	d.hub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
		Action:  string(calibration.ActionCancel),
		Message: fmt.Sprintf("Calibration canceled at phase %s and restored to previous state", st.Phase),
		Ts:      time.Now().Unix(),
	})

	d.calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	d.persistCalibrationState()
	d.releaseCalibrationSleepAssertion()
	return nil
}

// cancelCalibrationNoRestoreNoError cancels calibration without restoring previous
// charging state and does not return error if not running.
//
// It does restore the adapter and charging state to the snapshot values.
//
// This is used when disabling batt (limit=100%) to ensure calibration is not left
// in a non-idle state.
func (d *Daemon) cancelCalibrationNoRestoreNoError() {
	d.calibrationMu.Lock()
	defer d.calibrationMu.Unlock()
	if d.calibrationState.Phase == calibration.PhaseIdle {
		return
	}

	st := d.calibrationState
	var adapterErr error
	if st.SnapshotAdapterOn {
		adapterErr = d.enableAdapter()
	} else {
		adapterErr = d.disableAdapter()
	}
	if adapterErr != nil {
		logrus.WithError(adapterErr).Error("failed to restore adapter while canceling calibration")
		st.LastError = adapterErr.Error()
		st.Phase = calibration.PhaseError
		d.persistCalibrationState()
		return
	}

	d.hub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
		Action:  string(calibration.ActionCancel),
		Message: fmt.Sprintf("Calibration cancelled because at phase %s", st.Phase),
		Ts:      time.Now().Unix(),
	})

	d.calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	d.persistCalibrationState()
	d.releaseCalibrationSleepAssertion()
}

func (d *Daemon) getCalibrationStatus() *calibration.Status {
	d.calibrationMu.Lock()
	defer d.calibrationMu.Unlock()
	st := d.calibrationState
	charge, _ := d.smc.GetBatteryCharge()
	if charge < 0 {
		charge = 0
	} else if charge > 100 {
		charge = 100
	}
	plugged, _ := d.smc.IsPluggedIn()
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
			target = d.conf.UpperLimit()
		}
	}

	next, running := d.scheduler.Status()
	if !running {
		next = time.Time{}
	}

	return &calibration.Status{
		Phase: st.Phase, ChargePercent: charge, PluggedIn: plugged,
		RemainingHoldSecs: remain, StartedAt: st.StartedAt, Paused: st.Paused,
		CanPause:      st.Phase != calibration.PhaseIdle && st.Phase != calibration.PhaseRestore && st.Phase != calibration.PhaseError,
		CanCancel:     st.Phase != calibration.PhaseIdle,
		Message:       msg,
		TargetPercent: target,
		ScheduledAt:   next,
	}
}

// runScheduledCalibration is the scheduler's task.
func (d *Daemon) runScheduledCalibration() error {
	threshold := d.conf.CalibrationDischargeThreshold()
	hold := d.conf.CalibrationHoldDurationMinutes()
	return d.startCalibration(threshold, hold)
}

// scheduledCalibrationPreCheck is the scheduler's pre-check: it refuses to
// start a calibration that would be rejected or could not make progress.
func (d *Daemon) scheduledCalibrationPreCheck() error {
	status := d.getCalibrationStatus()
	if status.Phase != calibration.PhaseIdle {
		return ErrCalibrationInProgress
	}
	if !status.PluggedIn {
		return fmt.Errorf("the Mac must be plugged in to start calibration")
	}
	return nil
}

func (d *Daemon) notifyScheduleUpcoming(data any) {
	runAt := data.(time.Time)
	d.hub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
		Action:  string(calibration.ActionScheduleUpComing),
		Message: fmt.Sprintf("Calibration will start at %s", runAt.Format("Jan _2 15:04")),
		Ts:      time.Now().Unix(),
	})
}

func (d *Daemon) notifyScheduleError(data any) {
	err := data.(error)
	d.hub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
		Action:  string(calibration.ActionScheduleError),
		Message: err.Error(),
		Ts:      time.Now().Unix(),
	})
}

// schedule sets the cron expression for scheduled calibrations and returns the next run times.
func (d *Daemon) schedule(cronExpr string) ([]time.Time, error) {
	conf := d.conf
	if cronExpr == "" {
		prevCron := conf.Cron()
		if prevCron == "" {
			// Already disabled
			return nil, nil
		}

		conf.SetCron("")
		if err := conf.Save(); err != nil {
			logrus.WithError(err).Error("failed to save config")
			return nil, fmt.Errorf("failed to save config: %w", err)
		}
		d.scheduler.Stop()
		d.hub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
			Action:  string(calibration.ActionScheduleDisable),
			Message: "Calibration schedule disabled",
			Ts:      time.Now().Unix(),
		})
		return nil, nil
	}

	// Validate cron expression
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	sched, err := parser.Parse(cronExpr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}

	conf.SetCron(cronExpr)
	if err := conf.Save(); err != nil {
		logrus.WithError(err).Error("failed to save config")
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	if err := d.scheduler.Schedule(cronExpr); err != nil {
		logrus.WithError(err).Error("failed to schedule calibration")
		return nil, err
	}
	d.scheduler.Start()

	// generate three next run times for response
	nextRuns := []time.Time{}
	now := time.Now()
	for range 3 {
		next := sched.Next(now)
		nextRuns = append(nextRuns, next)
		now = next
	}

	d.hub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
		Action:  string(calibration.ActionSchedule),
		Message: fmt.Sprintf("Calibration scheduled at %s", nextRuns[0].Format("Jan _2 15:04")), // TODO: use cron descriptor
		Ts:      time.Now().Unix(),
	})

	return nextRuns, nil
}

func (d *Daemon) postpone(duration time.Duration) error {
	if err := d.scheduler.Postpone(duration); err != nil {
		logrus.WithError(err).Error("failed to postpone calibration")
		return err
	}

	d.hub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
		Action:  string(calibration.ActionSchedulePostpone),
		Message: fmt.Sprintf("Calibration postponed for %s", duration.String()),
		Ts:      time.Now().Unix(),
	})
	return nil
}

func (d *Daemon) skipNextSchedule() error {
	if err := d.scheduler.Skip(); err != nil {
		logrus.WithError(err).Error("failed to skip next scheduled calibration")
		return err
	}

	d.hub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
		Action:  string(calibration.ActionScheduleSkip),
		Message: "Calibration skipped",
		Ts:      time.Now().Unix(),
	})
	return nil
}
