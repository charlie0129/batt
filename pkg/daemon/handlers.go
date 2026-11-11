package daemon

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
	"github.com/charlie0129/batt/pkg/powerui"
	"github.com/charlie0129/batt/pkg/smc"
	"github.com/charlie0129/batt/pkg/version"
)

func (d *Daemon) getConfig(w http.ResponseWriter, r *http.Request) {
	fc, err := config.NewRawFileConfigFromConfig(d.conf)
	if err != nil {
		d.fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, fc)
}

func (d *Daemon) getLimit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.conf.UpperLimit())
}

func (d *Daemon) setLimit(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureChargingControl) {
		return
	}
	var l int
	if err := decodeJSON(r, &l); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	if l < 10 || l > 100 {
		err := fmt.Errorf("limit must be between 10 and 100, got %d", l)
		d.fail(w, http.StatusBadRequest, err)
		return
	}
	caps := d.getCapabilities()
	if !caps.SupportsLimit(l) {
		err := fmt.Errorf("this Mac only offers charge limits of %s, got %d", compatibility.FormatLimits(caps.SupportedLimits), l)
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	if d.calibrationOwnsChargeLimit() {
		err := ErrCalibrationControlsChargeLimit
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	conf := d.conf
	previousLimit, previousLower := conf.UpperLimit(), conf.LowerLimit()
	delta := previousLimit - previousLower
	if caps.ChargeControlMode != compatibility.ChargeControlNative && l-delta < 11 {
		err := fmt.Errorf("upper limit must be greater than lower limit + 10, got %d", l-delta)
		d.fail(w, http.StatusBadRequest, err)
		return
	}
	// Native control ignores the saved delta, but the shared config still
	// requires a nonnegative lower limit when changing the upper limit.
	if caps.ChargeControlMode == compatibility.ChargeControlNative && l < delta {
		conf.SetLowerLimit(previousLimit - (l - 1))
	}
	previousUntil, previousPreLimit := conf.DisableUntil(), conf.PreDisableLimit()
	conf.SetUpperLimit(l)
	// An explicit limit change overrides any pending scheduled re-enabling.
	conf.ClearDisableTimer()
	superseded := d.supersedeChargeOnce()
	if err := conf.Save(); err != nil {
		conf.SetUpperLimit(previousLimit)
		if conf.LowerLimit() != previousLower {
			conf.SetLowerLimit(previousLower)
		}
		if previousUntil.IsZero() {
			conf.ClearDisableTimer()
		} else {
			conf.SetDisableTimer(previousUntil, previousPreLimit)
		}
		if superseded != 0 {
			conf.SetChargeOnceTarget(superseded)
		}
		slog.Error("saveConfig failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}
	d.reportSupersededChargeOnce(superseded, fmt.Sprintf("charge limit set to %d%%", l))

	slog.Info(fmt.Sprintf("set charging limit to %d", l))

	var msg string
	charge, err := d.smc.GetBatteryCharge()
	switch {
	case d.usesNativeBand(conf.LowerLimit(), l):
		msg = fmt.Sprintf("set upper/lower charging limit to %d%%/%d%%", l, conf.LowerLimit())
		if err == nil {
			msg += fmt.Sprintf(", current charge: %d%%", charge)
		}
		msg += ". Between the limits the Mac runs from wall power without charging; it charges to the upper limit once the battery drops below the lower limit."
		if err == nil && charge > l {
			msg += " Current charge is above the limit; macOS may use battery power to discharge toward the target."
		}
	case caps.ChargeControlMode == compatibility.ChargeControlNative:
		msg = fmt.Sprintf("set charging limit to %d%%", l)
		if err == nil {
			msg += fmt.Sprintf(", current charge: %d%%", charge)
		}
		msg += ". macOS resumes charging once the battery drops about 5% below the limit."
		if err == nil && charge > l {
			msg += " Current charge is above the limit; macOS may use battery power to discharge toward the target."
		}
	case err != nil:
		msg = fmt.Sprintf("set upper/lower charging limit to %d%%/%d%%", conf.UpperLimit(), conf.LowerLimit())
	default:
		msg = fmt.Sprintf("set upper/lower charging limit to %d%%/%d%%, current charge: %d%%", conf.UpperLimit(), conf.LowerLimit(), charge)
		if charge > conf.UpperLimit() {
			switch {
			case d.isManagedChargeControl():
				msg += ". Current charge is above the limit; macOS may use battery power until it falls within the configured range."
			case caps.ChargeControlMode == compatibility.ChargeControlAdapter:
				msg += ". Current charge is above the limit, so batt will cut wall power and run from the battery until it drops to the lower limit."
			default:
				msg += ". Current charge is above the limit, so your computer will use power from the wall only. Battery charge will remain the same."
			}
		}
	}

	if l >= 100 {
		msg = "set charging limit to 100%. batt will not control charging anymore."
	}

	// Immediate single maintain loop, to avoid waiting for the next loop
	if applied := d.maintainLoopForced(); !applied && caps.NativeExactTargets {
		err := fmt.Errorf("limit %d%% was saved, but applying it failed; inspect the daemon log and battery status", l)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}
	if notice := d.pendingNativeLimitNotice(l); notice != "" {
		msg += " " + notice
	}

	writeJSON(w, http.StatusCreated, msg)
}

// resolveDisableLimit returns the limit to restore once a temporary disable
// elapses. A pending timer is rescheduled with the limit it already saved. It
// reports false when batt is disabled with no limit left to restore.
func resolveDisableLimit(conf config.Config) (int, bool) {
	if limit := conf.UpperLimit(); limit < 100 {
		return limit, true
	}

	if conf.DisableUntil().IsZero() {
		return 0, false
	}

	limit := conf.PreDisableLimit()
	if limit < 10 || limit > 100 {
		return 0, false
	}

	return limit, true
}

func (d *Daemon) setDisableFor(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureChargingControl) {
		return
	}
	var raw string
	if err := decodeJSON(r, &raw); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	dur, err := time.ParseDuration(raw)
	if err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	if dur <= 0 {
		err := fmt.Errorf("duration must be positive, got %s", dur)
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	if d.calibrationOwnsChargeLimit() {
		err := ErrCalibrationControlsChargeLimit
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	conf := d.conf
	prevLimit, ok := resolveDisableLimit(conf)
	if !ok {
		err := fmt.Errorf("batt is already disabled and no previous charge limit is recorded, nothing to restore. Set a limit first with 'batt limit <percentage>'")
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	until := time.Now().Add(dur).Truncate(time.Second)
	previousLimit := conf.UpperLimit()
	previousUntil, previousPreLimit := conf.DisableUntil(), conf.PreDisableLimit()
	superseded := d.supersedeChargeOnce()
	conf.SetUpperLimit(100)
	conf.SetDisableTimer(until, prevLimit)
	if err := conf.Save(); err != nil {
		conf.SetUpperLimit(previousLimit)
		if previousUntil.IsZero() {
			conf.ClearDisableTimer()
		} else {
			conf.SetDisableTimer(previousUntil, previousPreLimit)
		}
		if superseded != 0 {
			conf.SetChargeOnceTarget(superseded)
		}
		slog.Error("saveConfig failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}
	d.reportSupersededChargeOnce(superseded, "charge limit temporarily disabled")

	slog.Info("disabled batt temporarily",
		"until", until.Format(time.DateTime),
		"prevLimit", prevLimit,
	)

	if applied := d.maintainLoopForced(); !applied && d.getCapabilities().NativeExactTargets {
		err := fmt.Errorf("temporary disable was saved, but releasing charge control failed; the daemon will retry")
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, fmt.Sprintf("batt disabled, charge limit will be restored to %d%% at %s", prevLimit, until.Format(time.DateTime)))
}

// setBoolSetting is the shared body of the simple boolean setters: it binds
// the request, applies set, and persists the config.
func (d *Daemon) setBoolSetting(w http.ResponseWriter, r *http.Request, feature compatibility.Feature, name string, set func(bool)) {
	if !d.requireCapability(w, feature) {
		return
	}
	var value bool
	if err := decodeJSON(r, &value); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	set(value)
	if err := d.conf.Save(); err != nil {
		slog.Error("saveConfig failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	slog.Info(fmt.Sprintf("set %s to %t", name, value))

	writeJSON(w, http.StatusCreated, "ok")
}

func (d *Daemon) setPreventIdleSleep(w http.ResponseWriter, r *http.Request) {
	d.setBoolSetting(w, r, compatibility.FeatureSleepHooks, "prevent idle sleep", d.conf.SetPreventIdleSleep)
}

func (d *Daemon) setDisableChargingPreSleep(w http.ResponseWriter, r *http.Request) {
	d.setBoolSetting(w, r, compatibility.FeatureSleepHooks, "disable charging pre sleep", d.conf.SetDisableChargingPreSleep)
}

func (d *Daemon) setPreventSystemSleep(w http.ResponseWriter, r *http.Request) {
	d.setBoolSetting(w, r, compatibility.FeatureSleepHooks, "prevent system sleep", d.conf.SetPreventSystemSleep)
}

func (d *Daemon) setPreventSleepOnAdapterDisable(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureAdapterSleepPolicy) {
		return
	}
	var requested bool
	if err := decodeJSON(r, &requested); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	d.adapterPolicyMu.Lock()
	defer d.adapterPolicyMu.Unlock()

	adapterEnabled, err := d.smc.IsAdapterEnabled()
	if err != nil {
		slog.Error("failed to check adapter state for prevent-sleep setting", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	conf := d.conf
	current := conf.PreventSleepOnAdapterDisable()

	if requested {
		if !adapterEnabled {
			if err := d.holds.hold(sleepHoldAdapter); err != nil {
				slog.Error("failed to acquire sleep hold before saving config", "error", err)
				d.fail(w, http.StatusInternalServerError, err)
				return
			}
		}
		conf.SetPreventSleepOnAdapterDisable(true)
		if err := conf.Save(); err != nil {
			conf.SetPreventSleepOnAdapterDisable(current)
			if !adapterEnabled && !current {
				_ = d.holds.release(sleepHoldAdapter)
			}
			slog.Error("saveConfig failed", "error", err)
			d.fail(w, http.StatusInternalServerError, err)
			return
		}
	} else {
		conf.SetPreventSleepOnAdapterDisable(false)
		if err := conf.Save(); err != nil {
			conf.SetPreventSleepOnAdapterDisable(current)
			slog.Error("saveConfig failed", "error", err)
			d.fail(w, http.StatusInternalServerError, err)
			return
		}
		if err := d.holds.release(sleepHoldAdapter); err != nil {
			slog.Error("failed to release sleep hold after disabling setting", "error", err)
			d.fail(w, http.StatusInternalServerError, err)
			return
		}
	}

	slog.Info(fmt.Sprintf("set prevent sleep on adapter disable to %t", requested))

	writeJSON(w, http.StatusCreated, "ok")
}

func (d *Daemon) setAdapter(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureAdapterControl) {
		return
	}
	var enable bool
	if err := decodeJSON(r, &enable); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	if d.calibrationOwnsChargeLimit() {
		err := ErrCalibrationControlsAdapter
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	conf := d.conf
	// Cutting power stops a one-time charge from making any progress, and an
	// indefinite adapter disable records no deadline that could resume it.
	if !enable && conf.ChargeOnceTarget() != 0 {
		err := ErrChargeOnceInProgress
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	if enable {
		if err := d.enableAdapter(); err != nil {
			slog.Error("enablePowerAdapter failed", "error", err)
			d.fail(w, http.StatusInternalServerError, err)
			return
		}
		slog.Info("enabled power adapter")
	} else {
		if err := d.disableAdapter(); err != nil {
			slog.Error("disablePowerAdapter failed", "error", err)
			d.fail(w, http.StatusInternalServerError, err)
			return
		}
		slog.Info("disabled power adapter")
	}

	// An explicit adapter change overrides any pending scheduled enable.
	conf.ClearAdapterDisableTimer()
	if err := conf.Save(); err != nil {
		slog.Error("saveConfig failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, "ok")
}

func (d *Daemon) setAdapterDisableFor(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureAdapterControl) {
		return
	}
	var raw string
	if err := decodeJSON(r, &raw); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	dur, err := time.ParseDuration(raw)
	if err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}
	if dur <= 0 {
		err := fmt.Errorf("duration must be positive, got %s", dur)
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	if d.calibrationOwnsChargeLimit() {
		err := ErrCalibrationControlsAdapter
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	conf := d.conf
	// Cutting power stops a one-time charge from making any progress.
	if conf.ChargeOnceTarget() != 0 {
		err := ErrChargeOnceInProgress
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	until := time.Now().Add(dur).Truncate(time.Second)
	// Persist the recovery deadline before cutting power so a daemon crash
	// cannot leave the adapter disabled without a scheduled enable.
	conf.SetAdapterDisableTimer(until)
	if err := conf.Save(); err != nil {
		conf.ClearAdapterDisableTimer()
		slog.Error("saveConfig failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}
	if err := d.disableAdapter(); err != nil {
		conf.ClearAdapterDisableTimer()
		if saveErr := conf.Save(); saveErr != nil {
			slog.Error("failed to clear adapter disable timer after SMC error", "error", saveErr)
		}
		slog.Error("disablePowerAdapter failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	slog.Info("disabled power adapter temporarily", "until", until.Format(time.DateTime))
	writeJSON(w, http.StatusCreated, fmt.Sprintf("power adapter disabled, it will be enabled at %s", until.Format(time.DateTime)))
}

func (d *Daemon) getAdapter(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureAdapterControl) {
		return
	}
	enabled, err := d.smc.IsAdapterEnabled()
	if err != nil {
		slog.Error("getAdapter failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, enabled)
}

func (d *Daemon) getCharging(w http.ResponseWriter, r *http.Request) {
	caps := d.getCapabilities()
	if caps.ChargeControlMode != compatibility.ChargeControlLegacy {
		err := fmt.Errorf("direct charging state is not available in %s charge-control mode", caps.ChargeControlMode)
		d.fail(w, http.StatusConflict, err)
		return
	}
	charging, err := d.smc.IsChargingEnabled()
	if err != nil {
		slog.Error("getCharging failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, charging)
}

func (d *Daemon) getBatteryInfo(w http.ResponseWriter, r *http.Request) {
	b, err := d.battery.Snapshot()
	if err != nil {
		slog.Error("getBatteryInfo failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	var state powerinfo.BatteryState
	switch {
	case b.FullyCharged:
		state = powerinfo.Full
	case b.Charging:
		state = powerinfo.Charging
	default:
		state = powerinfo.Discharging
	}

	resp := powerinfo.Battery{
		State:          state,
		DesignCapacity: b.DesignCapacity,
		MaxCapacity:    b.MaxCapacity,
		ChargeRate:     int(math.Round(b.BatteryPower() * 1000)),
		DesignVoltage:  b.Voltage,
	}
	if d.getCapabilities().ChargeControlMode == compatibility.ChargeControlNative {
		if active, err := d.battery.FullChargeOverride(); err == nil {
			resp.ChargingToFullOverride = &active
		} else {
			slog.Debug("Full-charge override state unavailable", "error", err)
		}
	}
	if d.getCapabilities().NativeExactTargets {
		// macOS applies no charge limit on battery power, so nothing waits there.
		pending := d.pendingNativeLimit.Load() != 0 && !d.onBattery()
		resp.ChargeLimitPending = &pending
	}

	writeJSON(w, http.StatusOK, resp)
}

func (d *Daemon) setLowerLimitDelta(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureLowerLimit) {
		return
	}
	var delta int
	if err := decodeJSON(r, &delta); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	conf := d.conf
	if delta < 0 {
		err := fmt.Errorf("lower limit delta must be positive, got %d", delta)
		d.fail(w, http.StatusBadRequest, err)
		return
	}
	if conf.UpperLimit()-delta < 10 {
		err := fmt.Errorf("lower limit delta must be less than limit - 10, got %d", delta)
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	conf.SetLowerLimit(conf.UpperLimit() - delta)
	if err := conf.Save(); err != nil {
		slog.Error("saveConfig failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	ret := fmt.Sprintf("set lower limit delta to %d, current upper/lower limit is %d%%/%d%%", delta, conf.UpperLimit(), conf.LowerLimit())
	slog.Info(ret)
	d.maintainLoopForced()

	writeJSON(w, http.StatusCreated, ret)
}

func (d *Daemon) setControlMagSafeLED(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureMagSafeLED) {
		return
	}
	// Check if MasSafe is supported first. If not, return error.
	if !d.smc.CheckMagSafeExistence() {
		slog.Error("setControlMagSafeLED called but there is no MasSafe LED on this device")
		err := fmt.Errorf("there is no MasSafe on this device. You can only enable this setting on a compatible device, e.g. MacBook Pro 14-inch 2021")
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	var mode config.ControlMagSafeMode
	if err := decodeJSON(r, &mode); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	d.loopMu.Lock()
	defer d.loopMu.Unlock()
	if !d.requireCapability(w, compatibility.FeatureMagSafeLED) {
		return
	}

	d.conf.SetControlMagSafeLED(mode)
	if err := d.conf.Save(); err != nil {
		slog.Error("saveConfig failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	if d.isManagedChargeControl() && mode == config.ControlMagSafeModeDisabled {
		if err := d.smc.SetMagSafeLedState(smc.LEDSystem); err != nil {
			slog.Error("failed to return MagSafe LED control to the system", "error", err)
			d.fail(w, http.StatusInternalServerError, err)
			return
		}
	}

	slog.Info(fmt.Sprintf("set control MagSafe LED to %s", mode))

	writeJSON(w, http.StatusCreated, fmt.Sprintf("ControlMagSafeLED set to %s. You should be able to see the effect in a few minutes.", mode))
}

func (d *Daemon) getCurrentCharge(w http.ResponseWriter, r *http.Request) {
	charge, err := d.smc.GetBatteryCharge()
	if err != nil {
		slog.Error("getCurrentCharge failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, charge)
}

func (d *Daemon) getPluggedIn(w http.ResponseWriter, r *http.Request) {
	pluggedIn, err := d.smc.IsPluggedIn()
	if err != nil {
		slog.Error("getPluggedIn failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, pluggedIn)
}

func (d *Daemon) getChargingControlCapable(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.getCapabilities().ChargingControl)
}

func (d *Daemon) setAdapterMode(w http.ResponseWriter, r *http.Request) {
	var enabled bool
	if err := decodeJSON(r, &enabled); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	conf := d.conf
	previous := conf.AdapterMode()
	previousTarget := conf.ChargeOnceTarget()
	conf.SetAdapterMode(enabled)
	// Native macOS cannot force a one-time target below 100%. Cancel such an
	// adapter-mode target in the same config update as the mode change.
	clearTarget := previousTarget > 0 && previousTarget < 100 &&
		d.getCapabilities().ChargeControlMode == compatibility.ChargeControlAdapter &&
		d.detectCapabilities().ChargeControlMode == compatibility.ChargeControlNative
	if clearTarget {
		conf.ClearChargeOnceTarget()
	}
	if err := conf.Save(); err != nil {
		conf.SetAdapterMode(previous)
		if clearTarget {
			conf.SetChargeOnceTarget(previousTarget)
		}
		d.fail(w, http.StatusInternalServerError, err)
		return
	}
	if err := d.reapplyChargeControlMode(); err != nil {
		conf.SetAdapterMode(previous)
		if clearTarget {
			conf.SetChargeOnceTarget(previousTarget)
		}
		if saveErr := conf.Save(); saveErr != nil {
			err = fmt.Errorf("%w; failed to save adapter mode rollback: %v", err, saveErr)
		}
		d.fail(w, http.StatusInternalServerError, err)
		return
	}
	if clearTarget {
		d.reportSupersededChargeOnce(previousTarget, "adapter mode disabled")
	}
	writeJSON(w, http.StatusCreated, fmt.Sprintf("adapter mode set to %t, charge control is now %s", enabled, d.getCapabilities().ChargeControlMode))
}

func (d *Daemon) getVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, version.Version)
}

// powerTelemetry builds the GUI's simplified telemetry from a battery snapshot.
func (d *Daemon) powerTelemetry() (powerinfo.PowerTelemetry, error) {
	var telemetry powerinfo.PowerTelemetry
	b, err := d.battery.Snapshot()
	if err != nil {
		return telemetry, err
	}

	telemetry.Adapter.InputVoltage = b.InputVoltage
	telemetry.Adapter.InputAmperage = b.InputAmperage

	telemetry.Battery.CycleCount = b.CycleCount

	telemetry.Calculations.ACPower = b.AdapterPower()
	telemetry.Calculations.BatteryPower = b.BatteryPower()
	telemetry.Calculations.SystemPower = b.SystemPower()
	telemetry.Calculations.HealthByMaxCapacity = b.Health()
	return telemetry, nil
}

func (d *Daemon) getPowerTelemetry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Deprecated", "true")
	w.Header().Set("X-Deprecation-Info", "Use /telemetry?power=1 instead; /power-telemetry will be removed in a future release")
	snapshot, err := d.powerTelemetry()
	if err != nil {
		slog.Error("getPowerTelemetry failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, snapshot)
}

// Unified telemetry endpoint: /telemetry?power=1&calibration=1 (flags optional; default all)
func (d *Daemon) getUnifiedTelemetry(w http.ResponseWriter, r *http.Request) {
	wantPower := r.URL.Query().Get("power") != "0"
	wantCal := r.URL.Query().Get("calibration") != "0"

	resp := map[string]any{}

	if wantPower {
		snapshot, err := d.powerTelemetry()
		if err != nil {
			slog.Warn("power telemetry unavailable for unified telemetry", "error", err)
		} else {
			resp["power"] = snapshot
		}
	}

	if wantCal && d.getCapabilities().Calibration {
		resp["calibration"] = d.getCalibrationStatus()
	}

	// Add deprecation header if caller still hitting legacy endpoints (not detectable here), but we can add a generic hint.
	w.Header().Set("X-Batt-Telemetry-Version", "1")
	writeJSON(w, http.StatusOK, resp)
}

// SSE endpoint: streams daemon events (first: calibration phase changes)
func (d *Daemon) getEventStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		d.fail(w, http.StatusInternalServerError, errors.New("streaming is not supported"))
		return
	}

	ch := d.hub.Subscribe()
	defer d.hub.Unsubscribe(ch)

	// Notify client that stream is open and suggest retry interval
	if _, err := io.WriteString(w, "retry: 10000\n"); err != nil {
		slog.Warn("failed to write initial retry for SSE stream", "error", err)
		return
	}
	if _, err := io.WriteString(w, ":ok\n\n"); err != nil {
		slog.Warn("failed to write initial comment for SSE stream", "error", err)
		return
	}
	flusher.Flush()

	// Heartbeat ticker: send SSE comment periodically to keep the connection alive
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Stream loop
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// SSE comment line as heartbeat
			_, _ = io.WriteString(w, ":ping\n\n")
			flusher.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// SSE frame: event + data
			if msg.Name != "" {
				_, _ = io.WriteString(w, "event: "+msg.Name+"\n")
			}
			_, _ = io.WriteString(w, "data: ")
			_, _ = w.Write(msg.Data)
			_, _ = io.WriteString(w, "\n\n")
			flusher.Flush()
		}
	}
}

// ===== One-Time Charge Handlers =====

func (d *Daemon) postChargeOnceToLimit(w http.ResponseWriter, r *http.Request) {
	d.startChargeOnceRequest(w, r, false)
}

func (d *Daemon) postChargeOnceToFull(w http.ResponseWriter, r *http.Request) {
	d.startChargeOnceRequest(w, r, true)
}

func (d *Daemon) startChargeOnceRequest(w http.ResponseWriter, r *http.Request, full bool) {
	if !d.requireCapability(w, compatibility.FeatureChargingControl) {
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	// Conflicts come first: a pending temporary disable also reads as a
	// disabled charge limit, and naming the conflict tells the user what to do.
	if err := d.chargeOnceConflict(); err != nil {
		// A conflict is the caller's to resolve. A failed check is ours.
		var checkErr *chargeOnceCheckError
		if errors.As(err, &checkErr) {
			slog.Error("chargeOnceConflict failed", "error", err)
			d.fail(w, http.StatusInternalServerError, err)
			return
		}
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	conf := d.conf
	target, err := resolveChargeOnceTarget(conf, full)
	if err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}
	mode := d.getCapabilities().ChargeControlMode
	if !full && mode == compatibility.ChargeControlNative {
		d.fail(w, http.StatusConflict, ErrNativeChargeNow)
		return
	}

	charge, err := d.smc.GetBatteryCharge()
	if err != nil {
		slog.Error("GetBatteryCharge failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	// Admission asks the same question completion does, so a charge the next
	// maintain loop would end right away is refused instead of started.
	if d.chargeOnceReachedTarget(target, charge) {
		err := fmt.Errorf("battery is already at %d%%, so a one-time charge to %d%% would end immediately", charge, target)
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	// Adapter mode must not inherit a native macOS limit from a previous mode
	// or reboot. Remember the previous value in case admission fails.
	var nativePreviousLimit int
	var nativePreviouslyEnabled bool
	if mode == compatibility.ChargeControlAdapter && d.native.Supported() {
		var err error
		nativePreviousLimit, nativePreviouslyEnabled, err = d.native.Limit()
		if err == nil {
			_, err = d.ensureNativeChargeLimitDisabled()
		}
		if err != nil {
			d.fail(w, http.StatusInternalServerError, err)
			return
		}
	}
	restoreNativeLimit := func() error {
		if !nativePreviouslyEnabled {
			return nil
		}
		// A restored target that macOS applies later is still restored.
		if err := d.native.SetLimit(nativePreviousLimit); !errors.Is(err, powerui.ErrRegistrationPending) {
			return err
		}
		return nil
	}

	message := chargeOnceStartedMessage(target, charge, conf.UpperLimit())
	if err := d.startChargeOnce(target); err != nil {
		if restoreErr := restoreNativeLimit(); restoreErr != nil {
			err = fmt.Errorf("%w; failed to restore native limit: %v", err, restoreErr)
		}
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	// A successful API response requires the first enforcement pass to work.
	if !d.maintainLoopForced() {
		conf.ClearChargeOnceTarget()
		if err := conf.Save(); err != nil {
			conf.SetChargeOnceTarget(target)
			err = fmt.Errorf("failed to enforce one-time charge and roll back target: %w", err)
			d.fail(w, http.StatusInternalServerError, err)
			return
		}
		err := fmt.Errorf("failed to enforce one-time charge")
		if restoreErr := restoreNativeLimit(); restoreErr != nil {
			err = fmt.Errorf("%w; failed to restore native limit: %v", err, restoreErr)
		}
		d.fail(w, http.StatusInternalServerError, err)
		return
	}
	d.reportChargeOnceStarted(target, charge)
	writeJSON(w, http.StatusCreated, message)
}

func (d *Daemon) postCancelChargeOnce(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureChargingControl) {
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	target, err := d.cancelChargeOnce()
	if err != nil {
		// Only the missing one-time charge is the caller's fault; a failed save
		// is ours.
		if !errors.Is(err, ErrChargeOnceNotRunning) {
			slog.Error("saveConfig failed", "error", err)
			d.fail(w, http.StatusInternalServerError, err)
			return
		}
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	d.maintainLoopForced()

	msg := fmt.Sprintf("cancelled the one-time charge to %d%%, the %d%% charge limit applies again", target, d.conf.UpperLimit())
	if notice := d.pendingNativeLimitNotice(d.conf.UpperLimit()); notice != "" {
		msg += ". " + notice
	}
	writeJSON(w, http.StatusOK, msg)
}

// ===== Calibration Handlers =====

func (d *Daemon) postStartCalibration(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureCalibration) {
		return
	}
	// Read threshold & hold from current config getters
	threshold := d.conf.CalibrationDischargeThreshold()
	hold := d.conf.CalibrationHoldDurationMinutes()
	if err := d.startCalibration(threshold, hold); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func (d *Daemon) postPauseCalibration(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureCalibration) {
		return
	}
	if err := d.pauseCalibration(); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d *Daemon) postResumeCalibration(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureCalibration) {
		return
	}
	if err := d.resumeCalibration(); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d *Daemon) postCancelCalibration(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureCalibration) {
		return
	}
	if err := d.cancelCalibration(); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d *Daemon) setSchedule(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureCalibration) {
		return
	}
	var cronExpr string
	if err := decodeJSON(r, &cronExpr); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	nextRuns, err := d.schedule(cronExpr)
	if err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	resp := map[string]any{"ok": true}
	if nextRuns != nil {
		resp["next_runs"] = nextRuns
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (d *Daemon) skipSchedule(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureCalibration) {
		return
	}
	if err := d.skipNextSchedule(); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d *Daemon) postponeSchedule(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureCalibration) {
		return
	}
	var raw string
	if err := decodeJSON(r, &raw); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	// default 1 hour
	if raw == "" {
		raw = "1h"
	}

	dur, err := time.ParseDuration(raw)
	if err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	if err := d.postpone(dur); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d *Daemon) setCalibrationDischargeThreshold(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureCalibration) {
		return
	}
	var threshold int
	if err := decodeJSON(r, &threshold); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	if threshold < 10 || threshold > 50 {
		err := fmt.Errorf("calibration discharge threshold must be between 10 and 50, got %d", threshold)
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	d.conf.SetCalibrationDischargeThreshold(threshold)
	if err := d.conf.Save(); err != nil {
		slog.Error("saveConfig failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	slog.Info(fmt.Sprintf("set calibration discharge threshold to %d", threshold))

	// Check if calibration is running
	st := d.getCalibrationStatus()
	msg := fmt.Sprintf("Calibration discharge threshold set to %d%%", threshold)
	if st.Phase != calibration.PhaseIdle && st.Phase != calibration.PhaseRestore && st.Phase != calibration.PhaseError {
		msg += ". Note: A calibration is currently in progress. The new threshold will take effect on the next calibration."
	}

	writeJSON(w, http.StatusCreated, msg)
}

func (d *Daemon) setCalibrationHoldDurationMinutes(w http.ResponseWriter, r *http.Request) {
	if !d.requireCapability(w, compatibility.FeatureCalibration) {
		return
	}
	var minutes int
	if err := decodeJSON(r, &minutes); err != nil {
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	if minutes < 10 || minutes > 24*60 {
		err := fmt.Errorf("calibration hold duration must be between 10 and 1440 minutes (24 hours), got %d", minutes)
		d.fail(w, http.StatusBadRequest, err)
		return
	}

	d.conf.SetCalibrationHoldDurationMinutes(minutes)
	if err := d.conf.Save(); err != nil {
		slog.Error("saveConfig failed", "error", err)
		d.fail(w, http.StatusInternalServerError, err)
		return
	}

	slog.Info(fmt.Sprintf("set calibration hold duration to %d minutes", minutes))

	// Check if calibration is running
	st := d.getCalibrationStatus()
	msg := fmt.Sprintf("Calibration hold duration set to %d minutes", minutes)
	if st.Phase != calibration.PhaseIdle && st.Phase != calibration.PhaseRestore && st.Phase != calibration.PhaseError {
		msg += ". Note: A calibration is currently in progress. The new hold duration will take effect on the next calibration."
	}

	writeJSON(w, http.StatusCreated, msg)
}
