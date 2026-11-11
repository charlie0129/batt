package daemon

import (
	"log/slog"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/powerinfo"
	"github.com/charlie0129/batt/pkg/smc"
)

// isManagedChargeControl reports whether Apple, not batt, decides when to
// charge. The firmware and native backends only need the configured limit to
// be kept in sync; they never toggle charging themselves.
func (d *Daemon) isManagedChargeControl() bool {
	switch d.getCapabilities().ChargeControlMode {
	case compatibility.ChargeControlFirmware, compatibility.ChargeControlNative:
		return true
	default:
		return false
	}
}

// ensureManagedChargeLimit applies the configured limits to the active
// Apple-managed backend and reports whether anything had to be written.
func (d *Daemon) ensureManagedChargeLimit(lower, upper int) (bool, error) {
	switch d.getCapabilities().ChargeControlMode {
	case compatibility.ChargeControlFirmware:
		return d.smc.EnsureFirmwareChargeLimit(lower, upper)
	case compatibility.ChargeControlNative:
		return d.ensureNativeChargeLimit(upper)
	default:
		return false, smc.ErrNoChargingCapability
	}
}

// ensureManagedChargeLimitDisabled turns the Apple-managed limit off and
// reports whether it was on.
func (d *Daemon) ensureManagedChargeLimitDisabled() (bool, error) {
	switch d.getCapabilities().ChargeControlMode {
	case compatibility.ChargeControlFirmware:
		return d.smc.EnsureFirmwareChargeLimitDisabled()
	case compatibility.ChargeControlNative:
		return d.ensureNativeChargeLimitDisabled()
	default:
		return false, smc.ErrNoChargingCapability
	}
}

// ensureNativeChargeLimit keeps the macOS charge limit at the configured
// upper limit. Older native backends only offer a fixed set of slider values;
// macOS 27's preference path accepts exact percentages. The supported-value
// check is a safety net that never lowers the user's requested target. It
// returns powerui.ErrRegistrationPending while macOS has accepted the target
// but not applied it yet.
func (d *Daemon) ensureNativeChargeLimit(upper int) (bool, error) {
	caps := d.getCapabilities()
	if !caps.SupportsLimit(upper) {
		snapped := caps.NearestSupportedLimit(upper)
		slog.Debug("charge limit is not offered by macOS, using the next supported limit", "configured", upper, "limit", snapped)
		upper = snapped
	}
	if caps.NativeExactTargets && d.conf.NoDrain() {
		return d.ensureNoDrainLimit(upper)
	}
	var policyErr error
	if caps.NativeExactTargets {
		if err := d.releaseNoDrainLimit(); err != nil {
			if isNoDrainDeferred(err) {
				// Keep the old ceiling until powerd can be restarted safely.
				return false, err
			}
			// An unreadable policy store must not leave charging unmanaged.
			// Log the problem and use PowerUIAgent rather than guessing at the
			// contents of powerd's private preferences.
			policyErr = err
			slog.Error("could not remove the no-drain policy; using regular native control", "error", err)
		}
	}
	if upper >= 100 {
		changed, err := d.ensureNativeChargeLimitDisabled()
		if err != nil {
			return changed, err
		}
		return changed, noDrainFallbackError(policyErr)
	}

	current, enabled, err := d.native.Limit()
	if err != nil {
		return false, err
	}
	if enabled && current == upper {
		return false, noDrainFallbackError(policyErr)
	}
	if err := d.native.SetLimit(upper); err != nil {
		return false, err
	}
	return true, noDrainFallbackError(policyErr)
}

// nativeBandState tracks whether the native hysteresis band is charging the
// battery toward the upper limit or holding it where it is. known is false
// until the first pass after the band is (re)activated.
type nativeBandState struct {
	known    bool
	charging bool
	// target is the last target the band chose.
	target int
}

// usesNativeBand reports whether batt enforces the lower limit on top of the
// native macOS limit. See compatibility.Capabilities.UsesNativeBand.
func (d *Daemon) usesNativeBand(lower, upper int) bool {
	return d.getCapabilities().UsesNativeBand(lower, upper, d.conf.NoDrain())
}

// nativeBandTarget returns the native target that enforces the lower limit.
// macOS only resumes charging about 5% below its target, so on its own the
// native limit would top the battery up from upper-5 to upper over and over.
// Instead, while the battery is between the limits, batt sets the target to
// the current charge: macOS keeps running from wall power and the battery
// rests. Below the lower limit the target goes back to the upper limit and
// macOS charges to it. Because macOS retains the target, this also holds
// during sleep.
//
// On battery power macOS ignores the target, so it is left where it was
// rather than rewritten on every percent. The band catches up once the Mac
// is connected to power again.
func (d *Daemon) nativeBandTarget(lower, upper int) int {
	band := &d.nativeBand
	if d.onBattery() {
		if band.known {
			return band.target
		}
		return upper
	}

	charge, err := d.smc.GetBatteryCharge()
	if err != nil {
		// Without the charge batt cannot place the hold point. The upper
		// limit is always safe.
		slog.Error("GetBatteryCharge failed, using the upper limit as native target", "error", err)
		band.known = false
		return upper
	}

	if !band.known {
		// Resume a charge that was in progress when the band was activated,
		// as the legacy backend does: macOS was charging toward the upper
		// limit, or freely because no limit was set. The latter is the usual
		// case after a restart, since the daemon releases the limit on exit.
		current, enabled, err := d.native.Limit()
		band.charging = charge < lower || (err == nil && (!enabled || current >= upper) && charge < upper)
		band.known = true
	}

	switch {
	case charge < lower && !band.charging:
		slog.Info("battery charge is below the lower limit, charging to the upper limit", "batteryCharge", charge, "lower", lower, "upper", upper)
		band.charging = true
	case charge >= upper && band.charging:
		slog.Info("battery charge reached the upper limit, holding it", "batteryCharge", charge, "lower", lower, "upper", upper)
		band.charging = false
	}

	if band.charging {
		band.target = upper
	} else {
		// The hold point never rises above the upper limit, so a battery above
		// it (e.g. after Apple's periodic full charge) still drains toward the
		// limit.
		band.target = min(max(charge, lower, 10), upper)
	}
	return band.target
}

// nativeLimitOnBatteryNotice explains a native limit that macOS has accepted
// while the Mac runs on battery power, where macOS registers no charge limit.
const nativeLimitOnBatteryNotice = "macOS applies the limit once the Mac is connected to power."

// onBattery reports whether the Mac runs on battery power. After 5 minutes
// there, macOS withdraws a native charge limit until the Mac is connected to
// power again. An unreadable power source counts as wall power.
func (d *Daemon) onBattery() bool {
	pluggedIn, err := d.smc.IsPluggedIn()
	return err == nil && !pluggedIn
}

// pendingNativeLimitNotice explains limit if macOS has accepted it but not
// applied it yet, and returns "" otherwise. Only exact native mode keeps the
// pending target current; leaving it can leave a stale one behind.
func (d *Daemon) pendingNativeLimitNotice(limit int) string {
	if !d.getCapabilities().NativeExactTargets || int(d.pendingNativeLimit.Load()) != limit {
		return ""
	}
	if d.onBattery() {
		return nativeLimitOnBatteryNotice
	}
	return powerinfo.ChargeLimitPendingNotice
}

func (d *Daemon) ensureNativeChargeLimitDisabled() (bool, error) {
	var policyErr error
	if d.native.SupportsExactTargets() {
		if err := d.releaseNoDrainLimit(); err != nil {
			if isNoDrainDeferred(err) {
				return false, err
			}
			policyErr = err
		}
	}
	_, enabled, err := d.native.Limit()
	if err != nil {
		return false, err
	}
	if !enabled && !d.native.SupportsExactTargets() {
		return false, noDrainFallbackError(policyErr)
	}
	// The agent can say disabled while powerd still has its registration.
	// The client checks that independently and makes clean releases no-ops.
	if err := d.native.Disable(); err != nil {
		return enabled, err
	}
	return enabled, noDrainFallbackError(policyErr)
}

// resetChargeControl restores the platform's default charging behavior. It is
// used when the daemon exits.
func (d *Daemon) resetChargeControl() error {
	switch d.getCapabilities().ChargeControlMode {
	case compatibility.ChargeControlNative:
		_, err := d.ensureNativeChargeLimitDisabled()
		return err
	case compatibility.ChargeControlAdapter:
		// Restore wall power so the Mac is not left running from the battery.
		return d.enableAdapter()
	default:
		return d.smc.ResetChargeControl()
	}
}

func (d *Daemon) releaseChargeControlOnExit() error {
	caps := d.getCapabilities()
	if caps.NativeExactTargets {
		return d.stopChargeControl()
	}
	if caps.ChargingControl {
		return d.resetChargeControl()
	}
	return nil
}

// stopChargeControl waits for an in-flight maintenance pass, prevents queued
// passes from reapplying the limit, then releases charging for daemon shutdown.
func (d *Daemon) stopChargeControl() error {
	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()
	d.loopMu.Lock()
	defer d.loopMu.Unlock()
	d.chargeControlStopped = true
	if !d.getCapabilities().ChargingControl {
		return nil
	}
	return d.resetChargeControl()
}
