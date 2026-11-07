package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/charlie0129/batt/pkg/powerui"
)

var (
	errNoDrainDeferred = errors.New("no-drain update deferred to avoid interrupting another application's sleep prevention")
	errNoDrainFallback = errors.New("no-drain is unavailable; using the regular native charge limit")
)

func isNoDrainRestartPending(err error) bool {
	return errors.Is(err, powerui.ErrRestartDeferred) || errors.Is(err, powerui.ErrReloadPending)
}

func isNoDrainDeferred(err error) bool {
	return errors.Is(err, errNoDrainDeferred) || isNoDrainRestartPending(err)
}

func noDrainFallbackError(cause error) error {
	if cause == nil {
		return nil
	}
	return errors.Join(errNoDrainFallback, cause)
}

type noDrainPhase string

const (
	noDrainOff     noDrainPhase = "off"
	noDrainActive  noDrainPhase = "active"
	noDrainWaiting noDrainPhase = "waiting"
	noDrainError   noDrainPhase = "error"
)

// noDrainStatus is reported separately from Config because an opt-in setting
// can be saved while a powerd restart is waiting for sleep assertions to end.
type noDrainStatus struct {
	Enabled bool         `json:"enabled"`
	Phase   noDrainPhase `json:"phase"`
	Message string       `json:"message,omitempty"`
}

func (d *Daemon) setNoDrainStatus(phase noDrainPhase, message string) {
	d.noDrainState.Store(&noDrainStatus{Enabled: d.conf.NoDrain(), Phase: phase, Message: message})
}

func (d *Daemon) getNoDrainStatus() noDrainStatus {
	if state := d.noDrainState.Load(); state != nil {
		current := *state
		current.Enabled = d.conf.NoDrain()
		return current
	}
	return noDrainStatus{Enabled: d.conf.NoDrain(), Phase: noDrainOff}
}

// checkNoDrainRestart is deliberately called immediately before the only
// operation that restarts powerd. A restart drops assertions belonging to
// other applications, including caffeinate, and not all owners recreate them.
func (d *Daemon) checkNoDrainRestart() error {
	assertions, err := d.assertions.ForeignSleepAssertions()
	if err != nil {
		return fmt.Errorf("%w: cannot verify other applications' assertions: %v", errNoDrainDeferred, err)
	}
	if len(assertions) == 0 {
		return nil
	}
	parts := make([]string, 0, len(assertions))
	for _, assertion := range assertions {
		name := assertion.Name
		if name == "" {
			name = assertion.Type
		}
		parts = append(parts, fmt.Sprintf("pid %d (%s)", assertion.PID, name))
	}
	return fmt.Errorf("%w: %s", errNoDrainDeferred, strings.Join(parts, ", "))
}

func (d *Daemon) noDrainDeferred(upper int, policyActive bool, err error) (bool, error) {
	d.setNoDrainStatus(noDrainWaiting, err.Error())
	if policyActive {
		// Keep the existing ceiling rather than enabling a second policy while
		// waiting to change it. In particular, never charge above the old limit.
		return false, err
	}
	// There is no no-drain policy yet, so ordinary native control remains a
	// safe fallback until powerd can be restarted without dropping assertions.
	nativeErr := d.native.SetLimit(upper)
	if nativeErr != nil && !errors.Is(nativeErr, powerui.ErrRegistrationPending) {
		return false, nativeErr
	}
	// Preserve both reasons: the restart is deferred, and the ordinary native
	// target may itself still be waiting for powerd registration.
	return true, errors.Join(err, nativeErr)
}

func (d *Daemon) markNoDrainReloadPending(err error) error {
	d.noDrainReloadPending.Store(true)
	d.setNoDrainStatus(noDrainWaiting, err.Error())
	return err
}

func (d *Daemon) retryNoDrainReload() error {
	if err := d.native.ReloadNoDrain(); err != nil {
		if isNoDrainRestartPending(err) {
			return d.markNoDrainReloadPending(err)
		}
		d.setNoDrainStatus(noDrainError, err.Error())
		return err
	}
	d.noDrainReloadPending.Store(false)
	return nil
}

// releaseNoDrainLimit removes only batt's own powerd policy. It is also used
// on ordinary native passes so a previous opt-in cannot fight PowerUIAgent.
func (d *Daemon) releaseNoDrainLimit() error {
	if d.noDrainReloadPending.Load() {
		if err := d.retryNoDrainReload(); err != nil {
			return err
		}
	}
	_, enabled, err := d.native.NoDrainLimit()
	if err != nil {
		d.setNoDrainStatus(noDrainError, err.Error())
		return err
	}
	if !enabled {
		if !d.conf.NoDrain() {
			d.setNoDrainStatus(noDrainOff, "")
		}
		return nil
	}
	if err := d.checkNoDrainRestart(); err != nil {
		d.setNoDrainStatus(noDrainWaiting, err.Error())
		return err
	}
	if err := d.native.DisableNoDrain(); err != nil {
		if isNoDrainRestartPending(err) {
			return d.markNoDrainReloadPending(err)
		}
		d.setNoDrainStatus(noDrainError, err.Error())
		return err
	}
	d.setNoDrainStatus(noDrainOff, "")
	return nil
}

// noDrainFailure turns the opt-in off after a policy error and restores the
// regular native target. Keeping a broken opt-in enabled would retry a
// private-system write on every maintenance pass. Deferred restarts are not
// failures and leave the user's preference intact.
func (d *Daemon) noDrainFailure(cause error, upper int) (bool, error) {
	slog.Error("no-drain policy failed; falling back to regular native charging", "error", cause)
	d.conf.SetNoDrain(false)
	saveErr := d.conf.Save()
	d.setNoDrainStatus(noDrainError, cause.Error())

	_, policyActive, readErr := d.native.NoDrainLimit()
	if readErr != nil {
		return false, errors.Join(cause, saveErr, readErr)
	}
	if policyActive {
		if err := d.releaseNoDrainLimit(); err != nil {
			return false, errors.Join(cause, saveErr, err)
		}
	}
	changed := false
	var restoreErr error
	if upper >= 100 {
		restoreErr = d.native.Disable()
	} else {
		restoreErr = d.native.SetLimit(upper)
		changed = restoreErr == nil || errors.Is(restoreErr, powerui.ErrRegistrationPending)
	}
	if restoreErr != nil && !errors.Is(restoreErr, powerui.ErrRegistrationPending) {
		return changed, errors.Join(cause, saveErr, restoreErr)
	}
	d.setNoDrainStatus(noDrainError, cause.Error())
	return changed, errors.Join(errNoDrainFallback, cause, saveErr, restoreErr)
}

func (d *Daemon) ensureNoDrainLimit(upper int) (bool, error) {
	if d.noDrainReloadPending.Load() {
		if err := d.retryNoDrainReload(); err != nil {
			return false, err
		}
	}
	policyLimit, policyActive, err := d.native.NoDrainLimit()
	if err != nil {
		return d.noDrainFailure(err, upper)
	}
	if upper >= 100 {
		if policyActive {
			if err := d.releaseNoDrainLimit(); err != nil {
				return false, err
			}
		}
		d.setNoDrainStatus(noDrainOff, "")
		return false, d.native.Disable()
	}
	if policyActive && policyLimit == upper {
		// PowerUIAgent may have been re-enabled by System Settings or a power
		// source change. Its draining policy must not compete with ours.
		_, agentEnabled, err := d.native.Limit()
		if err != nil {
			return false, err
		}
		if agentEnabled {
			if err := d.native.Disable(); err != nil {
				return false, err
			}
		}
		d.setNoDrainStatus(noDrainActive, "powerd holds the ceiling without draining above the limit")
		return false, nil
	}

	if err := d.checkNoDrainRestart(); err != nil {
		return d.noDrainDeferred(upper, policyActive, err)
	}
	if err := d.native.SetNoDrainLimit(upper); err != nil {
		if isNoDrainRestartPending(err) {
			return false, d.markNoDrainReloadPending(err)
		}
		return d.noDrainFailure(err, upper)
	}
	d.setNoDrainStatus(noDrainActive, "powerd holds the ceiling without draining above the limit")
	return true, nil
}

// noDrainMaintenanceResult recognizes non-fatal no-drain outcomes. A deferred
// powerd restart or a successful fallback still leaves a valid charge policy;
// callers should keep maintenance alive and let the next pass finish the work.
func (d *Daemon) noDrainMaintenanceResult(err error, upper int, previous noDrainStatus) (bool, bool) {
	if err == nil || (!isNoDrainDeferred(err) && !errors.Is(err, errNoDrainFallback)) {
		return false, false
	}
	if errors.Is(err, powerui.ErrRegistrationPending) {
		d.pendingNativeLimit.Store(int32(upper))
	}
	current := d.getNoDrainStatus()
	if current.Phase != previous.Phase || current.Message != previous.Message {
		if isNoDrainDeferred(err) {
			slog.Info("no-drain update is waiting for powerd", "limit", upper, "reason", current.Message)
		} else {
			slog.Warn("using regular native charging because no-drain is unavailable", "reason", current.Message)
		}
	}
	return true, true
}
