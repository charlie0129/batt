package daemon

import (
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/events"
)

// A one-time charge charges the battery to a target percentage once and then
// hands control straight back to the configured charge limit.
//
// It never writes the configured limit. That keeps the two halves simple: there
// is nothing to restore when the target is reached, an interrupted daemon
// cannot leave a 100% limit behind in the config file, and the quick limits keep
// their usual meaning while a one-time charge runs.

const (
	chargeOnceActionStart    = "Start"
	chargeOnceActionCancel   = "Cancel"
	chargeOnceActionComplete = "Complete"
	chargeOnceLimitField     = "limit"
)

var (
	ErrChargeOnceInProgress = errors.New("a one-time charge is already in progress. Cancel it first with 'batt charge cancel'")
	ErrChargeOnceNotRunning = errors.New("no one-time charge is in progress")
	ErrChargeLimitDisabled  = errors.New("batt is not limiting charging, so a one-time charge would have no effect. Set a limit first with 'batt limit <percentage>'")
	ErrChargeLimitTooLow    = errors.New("the configured charge limit is below 10%, which batt does not support. Set a valid limit with 'batt limit <percentage>'")
	ErrNativeChargeNow      = errors.New("macOS controls when charging starts at a native charge limit; charging to the limit now is not available in this mode. Use 'batt charge full' instead")
	ErrAdapterDisabled      = errors.New("the power adapter is disabled, so a one-time charge would make no progress. Enable it first with 'batt adapter enable'")
)

// resolveChargeOnceTarget returns the charge percentage a one-time charge aims
// for. Both variants need a configured limit to hand control back to.
func resolveChargeOnceTarget(conf config.Config, full bool) (int, error) {
	limit := conf.UpperLimit()
	if limit >= 100 {
		return 0, ErrChargeLimitDisabled
	}
	// A hand-edited config can hold a limit batt never writes. Below 10% there
	// is no valid target to hand control back to, and storing one is rejected.
	if limit < 10 {
		return 0, ErrChargeLimitTooLow
	}
	if full {
		return 100, nil
	}
	return limit, nil
}

// chargeOnceConflict reports why a one-time charge cannot start right now.
func (d *Daemon) chargeOnceConflict() error {
	conf := d.conf
	if d.calibrationOwnsChargeLimit() {
		return ErrCalibrationControlsChargeLimit
	}
	if !conf.DisableUntil().IsZero() {
		return ErrTemporaryDisableInProgress
	}
	// A temporarily disabled power adapter cuts the power a one-time charge
	// needs, so the charge would silently make no progress.
	if !conf.AdapterDisableUntil().IsZero() {
		return ErrTemporaryAdapterDisableInProgress
	}
	if conf.ChargeOnceTarget() != 0 {
		return ErrChargeOnceInProgress
	}
	// "batt adapter disable" cuts the same power and writes no deadline, so the
	// config holds no trace of it. Only the hardware knows, and asking it comes
	// last because every check above answers from the config alone.
	if d.getCapabilities().AdapterControl {
		enabled, err := d.smc.IsAdapterEnabled()
		if err != nil {
			return &chargeOnceCheckError{fmt.Errorf("failed to read the power adapter state: %w", err)}
		}
		if !enabled {
			return ErrAdapterDisabled
		}
	}
	return nil
}

// chargeOnceCheckError marks a conflict that could not be decided because a
// check itself failed. The daemon failed, not the caller, so the request
// answers with a server error instead of a rejection.
type chargeOnceCheckError struct{ err error }

func (e *chargeOnceCheckError) Error() string { return e.err.Error() }
func (e *chargeOnceCheckError) Unwrap() error { return e.err }

// activeChargeOnceTarget returns the target of a running one-time charge, or 0.
// Calibration writes the charge limit itself, so a one-time charge persisted
// before a restart waits until calibration finishes or is cancelled.
func (d *Daemon) activeChargeOnceTarget() int {
	if d.calibrationOwnsChargeLimit() {
		return 0
	}
	return d.conf.ChargeOnceTarget()
}

// chargeOnceReachedTarget reports whether charge ends a one-time charge to
// target.
//
// The firmware API rejects lower >= upper, so the firmware backend drives a
// one-time charge below 100% with the narrowest legal band, target-1/target.
// The firmware resumes charging below the lower bound, so a battery sitting at
// target-1 never moves. Accepting target-1 as reached keeps that case from
// holding the narrowed band forever, which would also block a scheduled
// calibration. The price is that a firmware one-time charge can end one percent
// short of its target.
func (d *Daemon) chargeOnceReachedTarget(target, charge int) bool {
	if target < 100 && d.getCapabilities().ChargeControlMode == compatibility.ChargeControlFirmware {
		return charge >= target-1
	}
	return charge >= target
}

// chargeOnceStartedMessage describes what the daemon just started doing.
func chargeOnceStartedMessage(target, charge, limit int) string {
	if target >= 100 {
		return fmt.Sprintf("charging to 100%% once, currently at %d%%. The %d%% charge limit is restored automatically afterwards", charge, limit)
	}
	return fmt.Sprintf("charging to the %d%% charge limit now, currently at %d%%", target, charge)
}

// startChargeOnce persists a one-time charge to target. Callers hold
// transitionMu and have already resolved conflicts.
func (d *Daemon) startChargeOnce(target int) error {
	d.conf.SetChargeOnceTarget(target)
	if err := d.conf.Save(); err != nil {
		d.conf.ClearChargeOnceTarget()
		return err
	}

	return nil
}

// reportChargeOnceStarted runs only after the immediate enforcement succeeds.
func (d *Daemon) reportChargeOnceStarted(target, charge int) {
	logrus.WithFields(logrus.Fields{
		"target":             target,
		"charge":             charge,
		chargeOnceLimitField: d.conf.UpperLimit(),
	}).Info("started a one-time charge")
	d.publishChargeOnceEvent(chargeOnceActionStart, target, chargeOnceStartedMessage(target, charge, d.conf.UpperLimit()))
}

// cancelChargeOnce stops a running one-time charge and returns its target. The
// configured limit applies again immediately because it was never changed.
func (d *Daemon) cancelChargeOnce() (int, error) {
	conf := d.conf
	target := conf.ChargeOnceTarget()
	if target == 0 {
		return 0, ErrChargeOnceNotRunning
	}

	conf.ClearChargeOnceTarget()
	if err := conf.Save(); err != nil {
		// The file still holds the target, so keep it in memory too. Otherwise
		// a retry reports that nothing is running while a restart resumes the
		// one-time charge the user just cancelled.
		conf.SetChargeOnceTarget(target)
		return 0, err
	}

	logrus.WithFields(logrus.Fields{
		"target":             target,
		chargeOnceLimitField: conf.UpperLimit(),
	}).Info("cancelled the one-time charge")

	d.publishChargeOnceEvent(chargeOnceActionCancel, target,
		fmt.Sprintf("One-time charge cancelled. The %d%% charge limit applies again.", conf.UpperLimit()))
	return target, nil
}

// supersedeChargeOnce clears the in-memory target as part of a caller-owned
// config update. The caller restores it on save failure and reports the
// cancellation only after the new config has been persisted.
func (d *Daemon) supersedeChargeOnce() int {
	target := d.conf.ChargeOnceTarget()
	if target != 0 {
		d.conf.ClearChargeOnceTarget()
	}
	return target
}

func (d *Daemon) reportSupersededChargeOnce(target int, reason string) {
	if target == 0 {
		return
	}
	logrus.WithFields(logrus.Fields{"target": target, "reason": reason}).Info("cancelled the one-time charge")
	d.publishChargeOnceEvent(chargeOnceActionCancel, target,
		fmt.Sprintf("One-time charge to %d%% cancelled: %s.", target, reason))
}

// completeChargeOnce ends a one-time charge once the battery has reached its
// target, which hands control back to the configured limit. It reports whether
// it ended one.
func (d *Daemon) completeChargeOnce() bool {
	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	conf := d.conf
	target := conf.ChargeOnceTarget()
	if target == 0 {
		return false
	}
	// Calibration writes the charge limit itself. Keep the one-time charge
	// pending until calibration finishes or is cancelled.
	if d.calibrationOwnsChargeLimit() {
		return false
	}

	charge, err := d.smc.GetBatteryCharge()
	if err != nil {
		logrus.WithError(err).Error("failed to read battery charge for the one-time charge")
		return false
	}
	if !d.chargeOnceReachedTarget(target, charge) {
		return false
	}

	conf.ClearChargeOnceTarget()
	if err := conf.Save(); err != nil {
		// The file still holds the target. Keep memory consistent with it so the
		// next loop retries completion instead of reporting a false success.
		conf.SetChargeOnceTarget(target)
		logrus.Errorf("saveConfig failed: %v", err)
		return false
	}

	logrus.WithFields(logrus.Fields{
		"target":             target,
		"charge":             charge,
		chargeOnceLimitField: conf.UpperLimit(),
	}).Info("one-time charge reached its target, charge limit applies again")

	d.publishChargeOnceEvent(chargeOnceActionComplete, target,
		fmt.Sprintf("Charged to %d%%. The %d%% charge limit applies again.", charge, conf.UpperLimit()))
	return true
}

func (d *Daemon) publishChargeOnceEvent(action string, target int, message string) {
	d.hub.Publish(events.ChargeOnceAction, events.ChargeOnceActionEvent{
		Action:  action,
		Target:  target,
		Message: message,
		Ts:      time.Now().Unix(),
	})
}
