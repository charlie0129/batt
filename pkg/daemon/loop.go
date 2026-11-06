package daemon

import (
	"context"
	"reflect"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

// adapterMinBand is the minimum hysteresis band (upper - lower) enforced in
// adapter mode. Cutting wall power can only hold a ceiling by discharging and
// recharging, so a narrow band would cycle the battery and rewrite the SMC
// often. This floor only ever widens the band (lets the battery roam a little
// lower); it never raises the ceiling, so the limit is always respected.
const adapterMinBand = 5

// runLoop maintains the battery charge until ctx is cancelled.
func (d *Daemon) runLoop(ctx context.Context) {
	for {
		d.loopOnce(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-time.After(d.loopInterval):
		}
	}
}

// loopOnce is one iteration of the main loop: it finishes elapsed timers and
// one-time charges, keeps a temporary adapter disable enforced, then runs a
// maintenance pass.
func (d *Daemon) loopOnce(now time.Time) {
	if d.restoreDisabledLimit(now) {
		d.maintainLoopForced()
	}
	if d.completeChargeOnce() {
		d.maintainLoopForced()
	}
	if d.adapterSleepPolicyCapable() {
		d.maintainAdapterDisable(now)
	}
	d.maintainLoop()
}

// maintainAdapterDisable keeps a temporary adapter disable active across
// daemon restarts and enables the adapter once its deadline has passed. It
// reports whether an expired schedule was completed.
func (d *Daemon) maintainAdapterDisable(now time.Time) bool {
	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	if err := d.reconcileAdapterSleepPolicy(); err != nil {
		logrus.WithError(err).Error("failed to reconcile adapter sleep policy")
	}

	conf := d.conf
	until := conf.AdapterDisableUntil()
	if until.IsZero() {
		return false
	}
	// Calibration owns the adapter throughout its workflow and restores its
	// snapshot on completion or cancellation. Keep the timer pending until then.
	if d.calibrationOwnsChargeLimit() {
		return false
	}

	enabled, err := d.smc.IsAdapterEnabled()
	if err != nil {
		logrus.WithError(err).Error("failed to check power adapter for temporary disable")
		return false
	}

	if now.Before(until) {
		if err := d.disableAdapter(); err != nil {
			logrus.WithError(err).Error("failed to maintain temporary power adapter disable")
		}
		return false
	}

	if !enabled {
		if err := d.enableAdapter(); err != nil {
			logrus.WithError(err).Error("failed to enable power adapter after temporary disable")
			return false
		}
	} else {
		if err := d.holds.release(sleepHoldAdapter); err != nil {
			logrus.WithError(err).Error("failed to release sleep hold after temporary disable")
			return false
		}
	}
	conf.ClearAdapterDisableTimer()
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		return false
	}
	logrus.Info("adapter disable duration elapsed, power adapter enabled")
	return true
}

// restoreDisabledLimit restores the upper limit saved by a "batt disable --for"
// once its deadline has passed. It reports whether the limit was restored.
func (d *Daemon) restoreDisabledLimit(now time.Time) bool {
	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	conf := d.conf
	until := conf.DisableUntil()
	if until.IsZero() || now.Before(until) {
		return false
	}

	// Calibration force-writes the upper limit it snapshotted when it finishes,
	// which would silently undo the restore. This also resolves persisted
	// conflicts after a daemon restart: calibration keeps ownership and the
	// timer remains pending until calibration finishes or is cancelled.
	if d.calibrationOwnsChargeLimit() {
		return false
	}

	limit := conf.PreDisableLimit()
	conf.ClearDisableTimer()

	if limit < 10 || limit > 100 {
		logrus.Warnf("invalid saved charge limit %d%%, not restoring", limit)
		if err := conf.Save(); err != nil {
			logrus.Errorf("saveConfig failed: %v", err)
		}
		return false
	}
	caps := d.getCapabilities()
	if !caps.SupportsLimit(limit) {
		snapped := caps.NearestSupportedLimit(limit)
		logrus.WithFields(logrus.Fields{"saved": limit, "limit": snapped}).Warn("saved charge limit is not offered by this Mac, restoring the next supported limit")
		limit = snapped
	}

	conf.SetUpperLimit(limit)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
	}

	logrus.WithField("limit", limit).Infof("disable duration elapsed, charge limit restored")

	return true
}

// checkMissedMaintainLoops checks if there are too many missed maintain loops,
// which could indicate that the system was in sleep mode or there is some issue
// with the maintain loop execution.
// It returns true if there are too many missed loops.
func (d *Daemon) checkMissedMaintainLoops(logStatus bool) bool {
	maintainLoopCount := d.loopRecorder.GetRecordsIn(d.continuousLoopThreshold)
	expectedMaintainLoopCount := int(d.continuousLoopThreshold / d.loopInterval)
	minMaintainLoopCount := expectedMaintainLoopCount - 1
	relativeTimes := d.loopRecorder.GetLastRecords(d.continuousLoopThreshold)

	if maintainLoopCount < minMaintainLoopCount {
		if logStatus {
			logrus.WithFields(logrus.Fields{
				"maintainLoopCount":         maintainLoopCount,
				"expectedMaintainLoopCount": expectedMaintainLoopCount,
				"minMaintainLoopCount":      minMaintainLoopCount,
				"recentRecords":             formatRelativeTimes(relativeTimes),
			}).Infof("Possibly missed maintain loop")
		}

		return true
	}

	// Another loopInterval is added to the threshold so we can get
	// loop counts greater than minMaintainLoopCount, so we only print
	// once when the maintain loop is stabilized, instead of printing
	// every time when maintainLoopCount == minMaintainLoopCount (always
	// this case if using maintainLoopCount), which could be very spammy.
	if d.loopRecorder.GetRecordsIn(d.continuousLoopThreshold+d.loopInterval) == minMaintainLoopCount {
		if logStatus {
			logrus.WithFields(logrus.Fields{
				"maintainLoopCount":         maintainLoopCount,
				"expectedMaintainLoopCount": expectedMaintainLoopCount,
				"minMaintainLoopCount":      minMaintainLoopCount,
				"recentRecords":             formatRelativeTimes(relativeTimes),
			}).Infof("Maintain loop has been stabilized")
		}
	}

	return false
}

// maintainLoop maintains the battery charge. It has the logic to
// prevent parallel runs. So if one maintain loop is already running,
// the next one will need to wait until the first one finishes.
func (d *Daemon) maintainLoop() bool {
	if d.isManagedChargeControl() {
		return d.maintainLoopForced()
	}

	defer d.loopRecorder.AddRecordNow()

	if d.conf.PreventSystemSleep() {
		// No need to keep track missed loops and wait post/before sleep delays, since
		// prevent-system-sleep would prevent unexpected sleep during charging.
		return d.maintainLoopInner(true)
	}

	// See holdMaintainLoop in sleepcallback.go for why we need to wait.
	tsBeforeWait := time.Now()
	d.loopDelay.Wait()
	tsAfterWait := time.Now()
	if tsAfterWait.Sub(tsBeforeWait) > time.Second*1 {
		logrus.Debugf("this maintain loop waited %d seconds after being initiated, now ready to execute", int(tsAfterWait.Sub(tsBeforeWait).Seconds()))
	}

	// just log status, not doing anything, yet
	_ = d.checkMissedMaintainLoops(true)

	// Missed-loop protection is the fallback for sleep transitions where macOS
	// did not deliver a sleep notification. Disabling charging in that case is
	// part of DisableChargingPreSleep's behavior, so honor the user's choice to
	// let charging continue while the system is asleep.
	return d.maintainLoopInner(!d.conf.DisableChargingPreSleep())
}

// maintainLoopForced maintains the battery charge. It runs without waiting
// for post/pre sleep delays, but yet has logic to prevent parallel runs.
// It is mainly called by the HTTP APIs.
func (d *Daemon) maintainLoopForced() bool {
	return d.maintainLoopInner(true)
}

// maintainedCharging reports whether the last maintenance pass left charging
// on because the battery is below the limit.
func (d *Daemon) maintainedCharging() bool {
	d.loopMu.Lock()
	defer d.loopMu.Unlock()
	return d.maintainedChargingInProgress
}

func (d *Daemon) handleNoMaintain(isChargingEnabled bool) bool {
	if !isChargingEnabled {
		logrus.Debug("limit set to 100%, but charging is disabled, enabling")
		err := d.getCharger().Enable()
		if err != nil {
			logrus.Errorf("EnableCharging failed: %v", err)
			return false
		}

		if d.smc.CheckMagSafeExistence() {
			switch d.conf.ControlMagSafeLED() {
			case config.ControlMagSafeModeAlwaysOff:
				err := d.smc.DisableMagSafeLed()
				if err != nil {
					// no fail
					logrus.Errorf("DisableMagSafeLed failed: %v", err)
				}
			default:
				// Reset MagSafe LED to system state.
				err = d.smc.SetMagSafeLedState(smc.LEDSystem)
				if err != nil {
					// no fail
					logrus.Errorf("SetMagSafeLedState(LEDSystem) failed: %v", err)
				}
			}
		}
	}

	if d.smc.CheckMagSafeExistence() {
		// Set MagSafe LED according to config.
		currentMagSafeLEDState, err := d.smc.GetMagSafeLedState()
		if err != nil {
			// no fail
			logrus.Errorf("GetMagSafeLedState failed: %v", err)
		}
		switch d.conf.ControlMagSafeLED() {
		case config.ControlMagSafeModeAlwaysOff:
			if currentMagSafeLEDState != smc.LEDOff {
				err := d.smc.DisableMagSafeLed()
				if err != nil {
					// no fail
					logrus.Errorf("DisableMagSafeLed failed: %v", err)
				}
			}
		default:
			// This applies to both ControlMagSafeModeEnabled and ControlMagSafeModeDisabled
			// modes. Because in Disabled mode we should not interfere with the LED state,
			// in Enabled mode we want to show the system state (which is the same as
			// apple's default behavior when limit=100%).
			if currentMagSafeLEDState != smc.LEDSystem {
				err := d.smc.SetMagSafeLedState(smc.LEDSystem)
				if err != nil {
					// no fail
					logrus.Errorf("SetMagSafeLedState(LEDSystem) failed: %v", err)
				}
			}
		}
	}

	// Calling this multiple times is no-op.
	err := d.assertions.AllowSleepOnAC()
	if err != nil {
		logrus.Errorf("AllowSleepOnAC failed: %v", err)
	}

	d.cancelCalibrationForPermanentDisable()

	d.maintainedChargingInProgress = false
	return true
}

func (d *Daemon) cancelCalibrationForPermanentDisable() {
	// A persisted temporary-disable/calibration conflict can be loaded after a
	// restart. Calibration is restored paused for safety, so keep it intact and
	// let the disable timer wait for the user to resume or cancel calibration.
	if !d.conf.DisableUntil().IsZero() {
		return
	}
	d.cancelCalibrationNoRestoreNoError()
}

func (d *Daemon) handleChargingLogic(ignoreMissedLoops, isChargingEnabled, isPluggedIn bool, batteryCharge, lower, upper int) bool {
	maintainLoopsMissed := !ignoreMissedLoops && d.checkMissedMaintainLoops(false)

	// Fix for #123.
	// Consider this case:
	//   1. charging is enabled (batteryCharge < lower)
	//   2. batt execution is interrupted constantly (maintain loop is executed with unexpected interval), the maintain loop is missed for some reason (macOS didn't send a sleep notification)
	//   3. batt could not disable charging in-time because it's constantly interrupted, and the battery keeps charging, which could cause overcharging.
	//
	// In this case, we can stop charging immediately when we detect that
	// there are too many missed maintain loops, even if the battery charge is
	// below the lower limit, to prevent overcharging.
	if isChargingEnabled && maintainLoopsMissed {
		logrus.WithFields(logrus.Fields{
			"batteryCharge": batteryCharge,
			"lower":         lower,
			"upper":         upper,
		}).Infof("Too many missed maintain loops detected while charging is enabled. Disabling charging to prevent overcharging.")
		err := d.getCharger().Disable()
		if err != nil {
			logrus.Errorf("DisableCharging failed: %v", err)
			return false
		}
		isChargingEnabled = false
		d.maintainedChargingInProgress = false
	}

	// Should enable charging.
	if batteryCharge < lower && !isChargingEnabled {
		// If there are too many missed maintain loops, it could indicate that
		// the system was in sleep mode, or macOS interrupted executing the
		// maintain loop for some reason, or system has just woken up.
		// In this case, we should wait until the maintain loops are stable
		// before enabling charging, to avoid enabling charging when the
		// system is in sleep mode, which could overcharge.
		if !ignoreMissedLoops && maintainLoopsMissed {
			logrus.WithFields(logrus.Fields{
				"batteryCharge": batteryCharge,
				"lower":         lower,
				"upper":         upper,
			}).Infof("Battery charge is below lower limit, but too many missed maintain loops are missed. Will wait until maintain loops are stable")
			return true
		}

		logrus.WithFields(logrus.Fields{
			"batteryCharge": batteryCharge,
			"lower":         lower,
			"upper":         upper,
		}).Infof("Battery charge is below lower limit, enabling charging")
		err := d.getCharger().Enable()
		if err != nil {
			logrus.Errorf("EnableCharging failed: %v", err)
			return false
		}
		isChargingEnabled = true
		d.maintainedChargingInProgress = true
	}

	// Should disable charging.
	if batteryCharge >= upper && isChargingEnabled {
		logrus.WithFields(logrus.Fields{
			"batteryCharge": batteryCharge,
			"lower":         lower,
			"upper":         upper,
		}).Infof("Battery charge is above upper limit, disabling charging")
		err := d.getCharger().Disable()
		if err != nil {
			logrus.Errorf("DisableCharging failed: %v", err)
			return false
		}
		isChargingEnabled = false
		d.maintainedChargingInProgress = false
	}

	switch d.conf.ControlMagSafeLED() {
	case config.ControlMagSafeModeAlwaysOff:
		_ = d.smc.DisableMagSafeLed()
	case config.ControlMagSafeModeEnabled:
		d.updateMagSafeLed(isChargingEnabled)
	default:
		// nothing
	}

	if d.conf.PreventSystemSleep() {
		if isChargingEnabled {
			err := d.assertions.PreventSleepOnAC()
			if err != nil {
				logrus.Errorf("PreventSleepOnAC failed: %v", err)
			}
		} else {
			err := d.assertions.AllowSleepOnAC()
			if err != nil {
				logrus.Errorf("AllowSleepOnAC failed: %v", err)
			}
		}
	}

	// batteryCharge >= upper - delta && batteryCharge < upper
	// do nothing, keep as-is

	return true
}

func (d *Daemon) maintainLoopInner(ignoreMissedLoops bool) bool {
	d.loopMu.Lock()
	defer d.loopMu.Unlock()
	if d.chargeControlStopped {
		return true
	}

	switch d.getCapabilities().ChargeControlMode {
	case compatibility.ChargeControlFirmware, compatibility.ChargeControlNative:
		ok := d.maintainManagedChargeLimit()
		d.maintainManagedMagSafeLED()
		return ok
	case compatibility.ChargeControlLegacy, compatibility.ChargeControlAdapter:
		return d.maintainActiveCharging(ignoreMissedLoops)
	default:
		d.maintainedChargingInProgress = false
		return false
	}
}

// maintainManagedChargeLimit maintains Apple's charge control. Firmware takes
// both thresholds directly; native mode keeps the configured upper limit
// as the single target accepted by PowerUI.
func (d *Daemon) maintainManagedChargeLimit() bool {
	if d.calibrationNeedsMaintainLoop() {
		batteryCharge, err := d.smc.GetBatteryCharge()
		if err != nil {
			logrus.Errorf("GetBatteryCharge failed during calibration: %v", err)
			return false
		}
		if d.applyCalibrationWithinLoop(batteryCharge) {
			return true
		}
	}

	if target := d.activeChargeOnceTarget(); target > 0 {
		return d.maintainManagedChargeOnce(target)
	}

	mode := d.getCapabilities().ChargeControlMode
	upper := d.conf.UpperLimit()
	if upper >= 100 {
		changed, err := d.ensureManagedChargeLimitDisabled()
		if err != nil {
			logrus.Errorf("failed to deactivate %s charge limit: %v", mode, err)
			return false
		}
		if changed {
			logrus.Infof("deactivated %s charge limit", mode)
		}
		d.maintainedChargingInProgress = false
		return true
	}

	lower := d.conf.LowerLimit()
	changed, err := d.ensureManagedChargeLimit(lower, upper)
	if err != nil {
		logrus.Errorf("failed to reconcile %s charge limit: %v", mode, err)
		return false
	}
	if changed {
		logrus.WithFields(logrus.Fields{"lower": lower, "upper": upper}).Infof("reconciled %s charge limit", mode)
	} else {
		logrus.WithFields(logrus.Fields{"lower": lower, "upper": upper}).Tracef("%s charge limit is correct", mode)
	}
	d.maintainedChargingInProgress = false
	return true
}

// maintainManagedChargeOnce temporarily overrides the Apple-managed charge
// limit. Firmware supports a narrow band below the target; native macOS only
// supports charging to full once by temporarily disabling its limit.
func (d *Daemon) maintainManagedChargeOnce(target int) bool {
	d.maintainedChargingInProgress = false
	mode := d.getCapabilities().ChargeControlMode

	if target < 100 && mode != compatibility.ChargeControlFirmware {
		logrus.Error("the native macOS backend cannot force a one-time charge below 100%")
		return false
	}

	var changed bool
	var err error
	if target >= 100 {
		changed, err = d.ensureManagedChargeLimitDisabled()
	} else {
		changed, err = d.ensureManagedChargeLimit(target-1, target)
	}
	if err != nil {
		logrus.Errorf("failed to reconcile %s charge limit for a one-time charge: %v", mode, err)
		return false
	}
	if changed {
		logrus.WithField("target", target).Infof("reconciled %s charge limit for a one-time charge", mode)
	}
	return true
}

// maintainActiveCharging is the batt-managed hysteresis loop, shared by the
// legacy (charge-key) and adapter (wall-power) backends. The only difference
// between them is the charge switch it toggles.
func (d *Daemon) maintainActiveCharging(ignoreMissedLoops bool) bool {
	conf := d.conf
	upper := conf.UpperLimit()
	lower := conf.LowerLimit()
	mode := d.getCapabilities().ChargeControlMode
	// Adapter mode holds the ceiling by discharging on battery and recharging,
	// so keep a gentle minimum band to avoid frequent cycling and SMC writes.
	if mode == compatibility.ChargeControlAdapter {
		if minLower := upper - adapterMinBand; upper < 100 && lower > minLower && minLower >= 0 {
			lower = minLower
		}
	}
	maintain := upper < 100

	isChargingEnabled, err := d.getCharger().IsEnabled()
	if err != nil {
		logrus.Errorf("charger.IsEnabled failed: %v", err)
		return false
	}

	// Always get current battery charge to possibly drive calibration first.
	batteryCharge, err := d.smc.GetBatteryCharge()
	if err != nil {
		logrus.Errorf("GetBatteryCharge failed: %v", err)
		return false
	}

	isPluggedIn, err := d.smc.IsPluggedIn()
	if err != nil {
		logrus.Errorf("IsPluggedIn failed: %v", err)
		return false
	}

	chargeOnceTarget := d.activeChargeOnceTarget()
	if chargeOnceTarget > 0 && mode == compatibility.ChargeControlAdapter && d.native.Supported() {
		if _, err := d.ensureNativeChargeLimitDisabled(); err != nil {
			logrus.WithError(err).Error("failed to clear native limit for adapter-mode one-time charge")
			return false
		}
	}

	calibrating := d.calibrationOwnsChargeLimit()
	d.maintainedChargingInProgress = isChargingEnabled && isPluggedIn && !calibrating
	d.printStatus(batteryCharge, lower, upper, chargeOnceTarget, isChargingEnabled, isPluggedIn, d.maintainedChargingInProgress, calibrating)

	// If calibration is active, advance it and skip normal maintain logic.
	if d.applyCalibrationWithinLoop(batteryCharge) {
		switch conf.ControlMagSafeLED() {
		case config.ControlMagSafeModeAlwaysOff:
			_ = d.smc.DisableMagSafeLed()
		case config.ControlMagSafeModeEnabled:
			d.updateMagSafeLed(isChargingEnabled)
		default:
			// nothing
		}
		return true
	}

	// A one-time charge overrides the configured band until the battery reaches
	// its target. Passing the target as both bounds removes the hysteresis gap,
	// so charging starts right away instead of waiting for the charge to fall
	// below the lower limit, and stops exactly at the target.
	if chargeOnceTarget > 0 && batteryCharge < chargeOnceTarget {
		return d.handleChargingLogic(ignoreMissedLoops, isChargingEnabled, isPluggedIn, batteryCharge, chargeOnceTarget, chargeOnceTarget)
	}

	// If maintain is disabled, we don't care about the battery charge, enable charging anyway.
	if !maintain {
		return d.handleNoMaintain(isChargingEnabled)
	}

	// Adapter mode can only enforce the limit while on wall power. When running
	// from the battery there is nothing to cut or restore, so leave the adapter
	// untouched and wait for reconnection instead of writing the SMC pointlessly.
	// The next loop after reconnection resumes enforcement.
	if mode == compatibility.ChargeControlAdapter && !isPluggedIn {
		d.maintainedChargingInProgress = false
		return true
	}

	return d.handleChargingLogic(ignoreMissedLoops, isChargingEnabled, isPluggedIn, batteryCharge, lower, upper)
}

func (d *Daemon) updateMagSafeLed(isChargingEnabled bool) {
	err := d.smc.SetMagSafeCharging(isChargingEnabled)
	if err != nil {
		logrus.Errorf("SetMagSafeCharging failed: %v", err)
	}
}

type loopStatus struct {
	batteryCharge                int
	lower                        int
	upper                        int
	chargeOnceTarget             int
	isChargingEnabled            bool
	isPluggedIn                  bool
	maintainedChargingInProgress bool
	calibrationInProgress        bool
}

func (d *Daemon) printStatus(
	batteryCharge int,
	lower int,
	upper int,
	chargeOnceTarget int,
	isChargingEnabled bool,
	isPluggedIn bool,
	maintainedChargingInProgress bool,
	calibrationInProgress bool,
) {
	currentStatus := loopStatus{
		batteryCharge:                batteryCharge,
		lower:                        lower,
		upper:                        upper,
		chargeOnceTarget:             chargeOnceTarget,
		isChargingEnabled:            isChargingEnabled,
		isPluggedIn:                  isPluggedIn,
		maintainedChargingInProgress: maintainedChargingInProgress,
		calibrationInProgress:        calibrationInProgress,
	}

	fields := logrus.Fields{
		"batteryCharge":                batteryCharge,
		"lower":                        lower,
		"upper":                        upper,
		"chargeOnceTarget":             chargeOnceTarget,
		"chargingEnabled":              isChargingEnabled,
		"isPluggedIn":                  isPluggedIn,
		"maintainedChargingInProgress": maintainedChargingInProgress,
		"calibrationInProgress":        calibrationInProgress,
	}

	defer func() { d.lastPrintTime = time.Now() }()

	// Skip printing if the last print was less than loopInterval+1 seconds ago and everything is the same.
	if time.Since(d.lastPrintTime) < d.loopInterval+time.Second && reflect.DeepEqual(d.lastStatus, currentStatus) {
		logrus.WithFields(fields).Trace("status")
		return
	}

	logrus.WithFields(fields).Debug("status")

	d.lastStatus = currentStatus
}

// calibrationPhase returns the current calibration phase.
func (d *Daemon) calibrationPhase() calibration.Phase {
	d.calibrationMu.Lock()
	defer d.calibrationMu.Unlock()
	return d.calibrationState.Phase
}
