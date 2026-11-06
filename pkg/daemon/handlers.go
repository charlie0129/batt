package daemon

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
	"github.com/charlie0129/batt/pkg/smc"
	"github.com/charlie0129/batt/pkg/version"
)

func (d *Daemon) getConfig(c *gin.Context) {
	fc, err := config.NewRawFileConfigFromConfig(d.conf)
	if err != nil {
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.IndentedJSON(http.StatusOK, fc)
}

func (d *Daemon) getLimit(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, d.conf.UpperLimit())
}

func (d *Daemon) setLimit(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureChargingControl) {
		return
	}
	var l int
	if err := c.BindJSON(&l); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if l < 10 || l > 100 {
		err := fmt.Errorf("limit must be between 10 and 100, got %d", l)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	caps := d.getCapabilities()
	if !caps.SupportsLimit(l) {
		err := fmt.Errorf("this Mac only offers charge limits of %s, got %d", compatibility.FormatLimits(caps.SupportedLimits), l)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	if d.calibrationOwnsChargeLimit() {
		err := ErrCalibrationControlsChargeLimit
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf := d.conf
	previousLimit, previousLower := conf.UpperLimit(), conf.LowerLimit()
	delta := previousLimit - previousLower
	if caps.ChargeControlMode != compatibility.ChargeControlNative && l-delta < 11 {
		err := fmt.Errorf("upper limit must be greater than lower limit + 10, got %d", l-delta)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
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
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	d.reportSupersededChargeOnce(superseded, fmt.Sprintf("charge limit set to %d%%", l))

	logrus.Infof("set charging limit to %d", l)

	var msg string
	charge, err := d.smc.GetBatteryCharge()
	switch {
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
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusCreated, msg)
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

func (d *Daemon) setDisableFor(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureChargingControl) {
		return
	}
	var raw string
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	dur, err := time.ParseDuration(raw)
	if err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if dur <= 0 {
		err := fmt.Errorf("duration must be positive, got %s", dur)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	if d.calibrationOwnsChargeLimit() {
		err := ErrCalibrationControlsChargeLimit
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf := d.conf
	prevLimit, ok := resolveDisableLimit(conf)
	if !ok {
		err := fmt.Errorf("batt is already disabled and no previous charge limit is recorded, nothing to restore. Set a limit first with 'batt limit <percentage>'")
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
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
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	d.reportSupersededChargeOnce(superseded, "charge limit temporarily disabled")

	logrus.WithFields(logrus.Fields{
		"until":     until.Format(time.DateTime),
		"prevLimit": prevLimit,
	}).Infof("disabled batt temporarily")

	if applied := d.maintainLoopForced(); !applied && d.getCapabilities().NativeExactTargets {
		err := fmt.Errorf("temporary disable was saved, but releasing charge control failed; the daemon will retry")
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusCreated, fmt.Sprintf("batt disabled, charge limit will be restored to %d%% at %s", prevLimit, until.Format(time.DateTime)))
}

// setBoolSetting is the shared body of the simple boolean setters: it binds
// the request, applies set, and persists the config.
func (d *Daemon) setBoolSetting(c *gin.Context, feature compatibility.Feature, name string, set func(bool)) {
	if !d.requireCapability(c, feature) {
		return
	}
	var value bool
	if err := c.BindJSON(&value); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	set(value)
	if err := d.conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set %s to %t", name, value)

	c.IndentedJSON(http.StatusCreated, "ok")
}

func (d *Daemon) setPreventIdleSleep(c *gin.Context) {
	d.setBoolSetting(c, compatibility.FeatureSleepHooks, "prevent idle sleep", d.conf.SetPreventIdleSleep)
}

func (d *Daemon) setDisableChargingPreSleep(c *gin.Context) {
	d.setBoolSetting(c, compatibility.FeatureSleepHooks, "disable charging pre sleep", d.conf.SetDisableChargingPreSleep)
}

func (d *Daemon) setPreventSystemSleep(c *gin.Context) {
	d.setBoolSetting(c, compatibility.FeatureSleepHooks, "prevent system sleep", d.conf.SetPreventSystemSleep)
}

func (d *Daemon) setPreventSleepOnAdapterDisable(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureAdapterSleepPolicy) {
		return
	}
	var requested bool
	if err := c.BindJSON(&requested); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	d.adapterPolicyMu.Lock()
	defer d.adapterPolicyMu.Unlock()

	adapterEnabled, err := d.smc.IsAdapterEnabled()
	if err != nil {
		logrus.WithError(err).Error("failed to check adapter state for prevent-sleep setting")
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	conf := d.conf
	current := conf.PreventSleepOnAdapterDisable()

	if requested {
		if !adapterEnabled {
			if err := d.holds.hold(sleepHoldAdapter); err != nil {
				logrus.WithError(err).Error("failed to acquire sleep hold before saving config")
				c.IndentedJSON(http.StatusInternalServerError, err.Error())
				_ = c.AbortWithError(http.StatusInternalServerError, err)
				return
			}
		}
		conf.SetPreventSleepOnAdapterDisable(true)
		if err := conf.Save(); err != nil {
			conf.SetPreventSleepOnAdapterDisable(current)
			if !adapterEnabled && !current {
				_ = d.holds.release(sleepHoldAdapter)
			}
			logrus.Errorf("saveConfig failed: %v", err)
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	} else {
		conf.SetPreventSleepOnAdapterDisable(false)
		if err := conf.Save(); err != nil {
			conf.SetPreventSleepOnAdapterDisable(current)
			logrus.Errorf("saveConfig failed: %v", err)
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		if err := d.holds.release(sleepHoldAdapter); err != nil {
			logrus.WithError(err).Error("failed to release sleep hold after disabling setting")
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	}

	logrus.Infof("set prevent sleep on adapter disable to %t", requested)

	c.IndentedJSON(http.StatusCreated, "ok")
}

func (d *Daemon) setAdapter(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureAdapterControl) {
		return
	}
	var enable bool
	if err := c.BindJSON(&enable); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	if d.calibrationOwnsChargeLimit() {
		err := ErrCalibrationControlsAdapter
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf := d.conf
	// Cutting power stops a one-time charge from making any progress, and an
	// indefinite adapter disable records no deadline that could resume it.
	if !enable && conf.ChargeOnceTarget() != 0 {
		err := ErrChargeOnceInProgress
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if enable {
		if err := d.enableAdapter(); err != nil {
			logrus.Errorf("enablePowerAdapter failed: %v", err)
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		logrus.Infof("enabled power adapter")
	} else {
		if err := d.disableAdapter(); err != nil {
			logrus.Errorf("disablePowerAdapter failed: %v", err)
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		logrus.Infof("disabled power adapter")
	}

	// An explicit adapter change overrides any pending scheduled enable.
	conf.ClearAdapterDisableTimer()
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusCreated, "ok")
}

func (d *Daemon) setAdapterDisableFor(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureAdapterControl) {
		return
	}
	var raw string
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	dur, err := time.ParseDuration(raw)
	if err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	if dur <= 0 {
		err := fmt.Errorf("duration must be positive, got %s", dur)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	if d.calibrationOwnsChargeLimit() {
		err := ErrCalibrationControlsAdapter
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf := d.conf
	// Cutting power stops a one-time charge from making any progress.
	if conf.ChargeOnceTarget() != 0 {
		err := ErrChargeOnceInProgress
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	until := time.Now().Add(dur).Truncate(time.Second)
	// Persist the recovery deadline before cutting power so a daemon crash
	// cannot leave the adapter disabled without a scheduled enable.
	conf.SetAdapterDisableTimer(until)
	if err := conf.Save(); err != nil {
		conf.ClearAdapterDisableTimer()
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	if err := d.disableAdapter(); err != nil {
		conf.ClearAdapterDisableTimer()
		if saveErr := conf.Save(); saveErr != nil {
			logrus.Errorf("failed to clear adapter disable timer after SMC error: %v", saveErr)
		}
		logrus.Errorf("disablePowerAdapter failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.WithField("until", until.Format(time.DateTime)).Info("disabled power adapter temporarily")
	c.IndentedJSON(http.StatusCreated, fmt.Sprintf("power adapter disabled, it will be enabled at %s", until.Format(time.DateTime)))
}

func (d *Daemon) getAdapter(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureAdapterControl) {
		return
	}
	enabled, err := d.smc.IsAdapterEnabled()
	if err != nil {
		logrus.Errorf("getAdapter failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, enabled)
}

func (d *Daemon) getCharging(c *gin.Context) {
	caps := d.getCapabilities()
	if caps.ChargeControlMode != compatibility.ChargeControlLegacy {
		err := fmt.Errorf("direct charging state is not available in %s charge-control mode", caps.ChargeControlMode)
		c.IndentedJSON(http.StatusConflict, err.Error())
		_ = c.AbortWithError(http.StatusConflict, err)
		return
	}
	charging, err := d.smc.IsChargingEnabled()
	if err != nil {
		logrus.Errorf("getCharging failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, charging)
}

func (d *Daemon) getBatteryInfo(c *gin.Context) {
	// IOKit alone is sufficient here.
	info, err := d.battery.SystemInfo()
	if err != nil || info == nil || info.IOKit == nil {
		if err == nil {
			err = errors.New("no IOKit data available")
		}
		logrus.Errorf("getBatteryInfo failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Map powerkit-go data to our backwards-compatible Battery structure
	var state powerinfo.BatteryState
	switch {
	case info.IOKit.State.FullyCharged:
		state = powerinfo.Full
	case info.IOKit.State.IsCharging:
		state = powerinfo.Charging
	default:
		state = powerinfo.Discharging
	}

	// Compute charge rate (mW) using native amperage sign from IOKit
	powerW := info.IOKit.Battery.Voltage * info.IOKit.Battery.Amperage
	chargeRateMilliW := int(math.Round(powerW * 1000.0))

	resp := powerinfo.Battery{
		State:          state,
		DesignCapacity: info.IOKit.Battery.DesignCapacity,
		MaxCapacity:    info.IOKit.Battery.MaxCapacity,
		ChargeRate:     chargeRateMilliW,
		DesignVoltage:  info.IOKit.Battery.Voltage,
	}
	if d.getCapabilities().ChargeControlMode == compatibility.ChargeControlNative {
		if active, err := d.battery.FullChargeOverride(); err == nil {
			resp.ChargingToFullOverride = &active
		} else {
			logrus.WithError(err).Debug("Full-charge override state unavailable")
		}
	}

	c.IndentedJSON(http.StatusOK, resp)
}

func (d *Daemon) setLowerLimitDelta(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureLowerLimit) {
		return
	}
	var delta int
	if err := c.BindJSON(&delta); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf := d.conf
	if delta < 0 {
		err := fmt.Errorf("lower limit delta must be positive, got %d", delta)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	if conf.UpperLimit()-delta < 10 {
		err := fmt.Errorf("lower limit delta must be less than limit - 10, got %d", delta)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf.SetLowerLimit(conf.UpperLimit() - delta)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	ret := fmt.Sprintf("set lower limit delta to %d, current upper/lower limit is %d%%/%d%%", delta, conf.UpperLimit(), conf.LowerLimit())
	logrus.Info(ret)
	d.maintainLoopForced()

	c.IndentedJSON(http.StatusCreated, ret)
}

func (d *Daemon) setControlMagSafeLED(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureMagSafeLED) {
		return
	}
	// Check if MasSafe is supported first. If not, return error.
	if !d.smc.CheckMagSafeExistence() {
		logrus.Errorf("setControlMagSafeLED called but there is no MasSafe LED on this device")
		err := fmt.Errorf("there is no MasSafe on this device. You can only enable this setting on a compatible device, e.g. MacBook Pro 14-inch 2021")
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	var mode config.ControlMagSafeMode
	if err := c.BindJSON(&mode); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	d.loopMu.Lock()
	defer d.loopMu.Unlock()
	if !d.requireCapability(c, compatibility.FeatureMagSafeLED) {
		return
	}

	d.conf.SetControlMagSafeLED(mode)
	if err := d.conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	if d.isManagedChargeControl() && mode == config.ControlMagSafeModeDisabled {
		if err := d.smc.SetMagSafeLedState(smc.LEDSystem); err != nil {
			logrus.WithError(err).Error("failed to return MagSafe LED control to the system")
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	}

	logrus.Infof("set control MagSafe LED to %s", mode)

	c.IndentedJSON(http.StatusCreated, fmt.Sprintf("ControlMagSafeLED set to %s. You should be able to see the effect in a few minutes.", mode))
}

func (d *Daemon) getCurrentCharge(c *gin.Context) {
	charge, err := d.smc.GetBatteryCharge()
	if err != nil {
		logrus.Errorf("getCurrentCharge failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, charge)
}

func (d *Daemon) getPluggedIn(c *gin.Context) {
	pluggedIn, err := d.smc.IsPluggedIn()
	if err != nil {
		logrus.Errorf("getPluggedIn failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, pluggedIn)
}

func (d *Daemon) getChargingControlCapable(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, d.getCapabilities().ChargingControl)
}

func (d *Daemon) setAdapterMode(c *gin.Context) {
	var enabled bool
	if err := c.BindJSON(&enabled); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
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
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
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
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		return
	}
	if clearTarget {
		d.reportSupersededChargeOnce(previousTarget, "adapter mode disabled")
	}
	c.IndentedJSON(http.StatusCreated, fmt.Sprintf("adapter mode set to %t, charge control is now %s", enabled, d.getCapabilities().ChargeControlMode))
}

func (d *Daemon) getVersion(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, version.Version)
}

// powerTelemetry builds the GUI's simplified telemetry from an IOKit snapshot.
func (d *Daemon) powerTelemetry() (powerinfo.PowerTelemetry, error) {
	var snapshot powerinfo.PowerTelemetry
	info, err := d.battery.SystemInfo()
	if err != nil || info == nil || info.IOKit == nil {
		if err == nil {
			err = errors.New("failed to fetch IOKit power data")
		}
		return snapshot, err
	}

	snapshot.Adapter.InputVoltage = info.IOKit.Adapter.InputVoltage
	snapshot.Adapter.InputAmperage = info.IOKit.Adapter.InputAmperage

	snapshot.Battery.CycleCount = info.IOKit.Battery.CycleCount

	snapshot.Calculations.ACPower = info.IOKit.Calculations.AdapterPower
	snapshot.Calculations.BatteryPower = info.IOKit.Calculations.BatteryPower
	snapshot.Calculations.SystemPower = info.IOKit.Calculations.SystemPower
	snapshot.Calculations.HealthByMaxCapacity = info.IOKit.Calculations.HealthByMaxCapacity
	return snapshot, nil
}

func (d *Daemon) getPowerTelemetry(c *gin.Context) {
	c.Header("X-Deprecated", "true")
	c.Header("X-Deprecation-Info", "Use /telemetry?power=1 instead; /power-telemetry will be removed in a future release")
	snapshot, err := d.powerTelemetry()
	if err != nil {
		logrus.Errorf("getPowerTelemetry failed: %v", err)
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, snapshot)
}

// Unified telemetry endpoint: /telemetry?power=1&calibration=1 (flags optional; default all)
func (d *Daemon) getUnifiedTelemetry(c *gin.Context) {
	wantPower := c.Query("power") != "0"
	wantCal := c.Query("calibration") != "0"

	resp := gin.H{}

	if wantPower {
		snapshot, err := d.powerTelemetry()
		if err != nil {
			logrus.WithError(err).Warn("power telemetry unavailable for unified telemetry")
		} else {
			resp["power"] = snapshot
		}
	}

	if wantCal && d.getCapabilities().Calibration {
		resp["calibration"] = d.getCalibrationStatus()
	}

	// Add deprecation header if caller still hitting legacy endpoints (not detectable here), but we can add a generic hint.
	c.Header("X-Batt-Telemetry-Version", "1")
	c.IndentedJSON(http.StatusOK, resp)
}

// SSE endpoint: streams daemon events (first: calibration phase changes)
func (d *Daemon) getEventStream(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.Status(http.StatusInternalServerError)
		return
	}

	ch := d.hub.Subscribe()
	defer d.hub.Unsubscribe(ch)

	// Notify client that stream is open and suggest retry interval
	if _, err := c.Writer.WriteString("retry: 10000\n"); err != nil {
		logrus.WithError(err).Warn("failed to write initial retry for SSE stream")
		return
	}
	if _, err := c.Writer.WriteString(":ok\n\n"); err != nil {
		logrus.WithError(err).Warn("failed to write initial comment for SSE stream")
		return
	}
	flusher.Flush()

	// Heartbeat ticker: send SSE comment periodically to keep the connection alive
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Stream loop
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			// SSE comment line as heartbeat
			_, _ = c.Writer.WriteString(":ping\n\n")
			flusher.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// SSE frame: event + data
			if msg.Name != "" {
				_, _ = c.Writer.WriteString("event: " + msg.Name + "\n")
			}
			_, _ = c.Writer.WriteString("data: ")
			_, _ = c.Writer.Write(msg.Data)
			_, _ = c.Writer.WriteString("\n\n")
			flusher.Flush()
		}
	}
}

// ===== One-Time Charge Handlers =====

func (d *Daemon) postChargeOnceToLimit(c *gin.Context) { d.startChargeOnceRequest(c, false) }

func (d *Daemon) postChargeOnceToFull(c *gin.Context) { d.startChargeOnceRequest(c, true) }

func (d *Daemon) startChargeOnceRequest(c *gin.Context, full bool) {
	if !d.requireCapability(c, compatibility.FeatureChargingControl) {
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
			logrus.Errorf("chargeOnceConflict failed: %v", err)
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf := d.conf
	target, err := resolveChargeOnceTarget(conf, full)
	if err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	mode := d.getCapabilities().ChargeControlMode
	if !full && mode == compatibility.ChargeControlNative {
		c.IndentedJSON(http.StatusConflict, ErrNativeChargeNow.Error())
		_ = c.AbortWithError(http.StatusConflict, ErrNativeChargeNow)
		return
	}

	charge, err := d.smc.GetBatteryCharge()
	if err != nil {
		logrus.Errorf("GetBatteryCharge failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Admission asks the same question completion does, so a charge the next
	// maintain loop would end right away is refused instead of started.
	if d.chargeOnceReachedTarget(target, charge) {
		err := fmt.Errorf("battery is already at %d%%, so a one-time charge to %d%% would end immediately", charge, target)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
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
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	}
	restoreNativeLimit := func() error {
		if nativePreviouslyEnabled {
			return d.native.SetLimit(nativePreviousLimit)
		}
		return nil
	}

	message := chargeOnceStartedMessage(target, charge, conf.UpperLimit())
	if err := d.startChargeOnce(target); err != nil {
		if restoreErr := restoreNativeLimit(); restoreErr != nil {
			err = fmt.Errorf("%w; failed to restore native limit: %v", err, restoreErr)
		}
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// A successful API response requires the first enforcement pass to work.
	if !d.maintainLoopForced() {
		conf.ClearChargeOnceTarget()
		if err := conf.Save(); err != nil {
			conf.SetChargeOnceTarget(target)
			err = fmt.Errorf("failed to enforce one-time charge and roll back target: %w", err)
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		err := fmt.Errorf("failed to enforce one-time charge")
		if restoreErr := restoreNativeLimit(); restoreErr != nil {
			err = fmt.Errorf("%w; failed to restore native limit: %v", err, restoreErr)
		}
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	d.reportChargeOnceStarted(target, charge)
	c.IndentedJSON(http.StatusCreated, message)
}

func (d *Daemon) postCancelChargeOnce(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureChargingControl) {
		return
	}

	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()

	target, err := d.cancelChargeOnce()
	if err != nil {
		// Only the missing one-time charge is the caller's fault; a failed save
		// is ours.
		if !errors.Is(err, ErrChargeOnceNotRunning) {
			logrus.Errorf("saveConfig failed: %v", err)
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	d.maintainLoopForced()

	c.IndentedJSON(http.StatusOK, fmt.Sprintf("cancelled the one-time charge to %d%%, the %d%% charge limit applies again", target, d.conf.UpperLimit()))
}

// ===== Calibration Handlers =====

func (d *Daemon) postStartCalibration(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	// Read threshold & hold from current config getters
	threshold := d.conf.CalibrationDischargeThreshold()
	hold := d.conf.CalibrationHoldDurationMinutes()
	if err := d.startCalibration(threshold, hold); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	c.IndentedJSON(http.StatusCreated, gin.H{"ok": true})
}

func (d *Daemon) postPauseCalibration(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	if err := d.pauseCalibration(); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Daemon) postResumeCalibration(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	if err := d.resumeCalibration(); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Daemon) postCancelCalibration(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	if err := d.cancelCalibration(); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Daemon) setSchedule(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	var cronExpr string
	if err := c.BindJSON(&cronExpr); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	nextRuns, err := d.schedule(cronExpr)
	if err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	resp := gin.H{"ok": true}
	if nextRuns != nil {
		resp["next_runs"] = nextRuns
	}

	c.IndentedJSON(http.StatusCreated, resp)
}

func (d *Daemon) skipSchedule(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	if err := d.skipNextSchedule(); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Daemon) postponeSchedule(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	var raw string
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	// default 1 hour
	if raw == "" {
		raw = "1h"
	}

	dur, err := time.ParseDuration(raw)
	if err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if err := d.postpone(dur); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Daemon) setCalibrationDischargeThreshold(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	var threshold int
	if err := c.BindJSON(&threshold); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if threshold < 10 || threshold > 50 {
		err := fmt.Errorf("calibration discharge threshold must be between 10 and 50, got %d", threshold)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	d.conf.SetCalibrationDischargeThreshold(threshold)
	if err := d.conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set calibration discharge threshold to %d", threshold)

	// Check if calibration is running
	st := d.getCalibrationStatus()
	msg := fmt.Sprintf("Calibration discharge threshold set to %d%%", threshold)
	if st.Phase != calibration.PhaseIdle && st.Phase != calibration.PhaseRestore && st.Phase != calibration.PhaseError {
		msg += ". Note: A calibration is currently in progress. The new threshold will take effect on the next calibration."
	}

	c.IndentedJSON(http.StatusCreated, msg)
}

func (d *Daemon) setCalibrationHoldDurationMinutes(c *gin.Context) {
	if !d.requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	var minutes int
	if err := c.BindJSON(&minutes); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if minutes < 10 || minutes > 24*60 {
		err := fmt.Errorf("calibration hold duration must be between 10 and 1440 minutes (24 hours), got %d", minutes)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	d.conf.SetCalibrationHoldDurationMinutes(minutes)
	if err := d.conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set calibration hold duration to %d minutes", minutes)

	// Check if calibration is running
	st := d.getCalibrationStatus()
	msg := fmt.Sprintf("Calibration hold duration set to %d minutes", minutes)
	if st.Phase != calibration.PhaseIdle && st.Phase != calibration.PhaseRestore && st.Phase != calibration.PhaseError {
		msg += ". Note: A calibration is currently in progress. The new hold duration will take effect on the next calibration."
	}

	c.IndentedJSON(http.StatusCreated, msg)
}
