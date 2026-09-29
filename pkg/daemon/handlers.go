package daemon

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/peterneutron/powerkit-go/pkg/powerkit"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
	"github.com/charlie0129/batt/pkg/version"
)

func getConfig(c *gin.Context) {
	fc, err := config.NewRawFileConfigFromConfig(conf)
	if err != nil {
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.IndentedJSON(http.StatusOK, fc)
}

func getLimit(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, conf.UpperLimit())
}

func setLimit(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureChargingControl) {
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
	caps := getCapabilities()
	if !caps.SupportsLimit(l) {
		err := fmt.Errorf("this Mac only offers charge limits of %s, got %d", compatibility.FormatLimits(caps.SupportedLimits), l)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	chargeControlTransitionMu.Lock()
	defer chargeControlTransitionMu.Unlock()

	if calibrationOwnsChargeLimit() {
		err := ErrCalibrationControlsChargeLimit
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if delta := conf.UpperLimit() - conf.LowerLimit(); l-delta <= 10 {
		err := fmt.Errorf("upper limit must be greater than lower limit + 10, got %d", l-delta)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	previousLimit := conf.UpperLimit()
	previousUntil, previousPreLimit := conf.DisableUntil(), conf.PreDisableLimit()
	conf.SetUpperLimit(l)
	// An explicit limit change overrides any pending scheduled re-enabling.
	conf.ClearDisableTimer()
	superseded := supersedeChargeOnce()
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
	reportSupersededChargeOnce(superseded, fmt.Sprintf("charge limit set to %d%%", l))

	logrus.Infof("set charging limit to %d", l)

	var msg string
	charge, err := smcConn.GetBatteryCharge()
	if err != nil {
		msg = fmt.Sprintf("set upper/lower charging limit to %d%%/%d%%", conf.UpperLimit(), conf.LowerLimit())
	} else {
		msg = fmt.Sprintf("set upper/lower charging limit to %d%%/%d%%, current charge: %d%%", conf.UpperLimit(), conf.LowerLimit(), charge)
		if charge > conf.UpperLimit() {
			switch {
			case isManagedChargeControl():
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
	maintainLoopForced()

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

func setDisableFor(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureChargingControl) {
		return
	}
	var raw string
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if d <= 0 {
		err := fmt.Errorf("duration must be positive, got %s", d)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	chargeControlTransitionMu.Lock()
	defer chargeControlTransitionMu.Unlock()

	if calibrationOwnsChargeLimit() {
		err := ErrCalibrationControlsChargeLimit
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	prevLimit, ok := resolveDisableLimit(conf)
	if !ok {
		err := fmt.Errorf("batt is already disabled and no previous charge limit is recorded, nothing to restore. Set a limit first with 'batt limit <percentage>'")
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	until := time.Now().Add(d).Truncate(time.Second)
	previousLimit := conf.UpperLimit()
	previousUntil, previousPreLimit := conf.DisableUntil(), conf.PreDisableLimit()
	superseded := supersedeChargeOnce()
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
	reportSupersededChargeOnce(superseded, "charge limit temporarily disabled")

	logrus.WithFields(logrus.Fields{
		"until":     until.Format(time.DateTime),
		"prevLimit": prevLimit,
	}).Infof("disabled batt temporarily")

	maintainLoopForced()

	c.IndentedJSON(http.StatusCreated, fmt.Sprintf("batt disabled, charge limit will be restored to %d%% at %s", prevLimit, until.Format(time.DateTime)))
}

func setPreventIdleSleep(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureSleepHooks) {
		return
	}
	var p bool
	if err := c.BindJSON(&p); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf.SetPreventIdleSleep(p)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set prevent idle sleep to %t", p)

	c.IndentedJSON(http.StatusCreated, "ok")
}

func setDisableChargingPreSleep(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureSleepHooks) {
		return
	}
	var d bool
	if err := c.BindJSON(&d); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf.SetDisableChargingPreSleep(d)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set disable charging pre sleep to %t", d)

	c.IndentedJSON(http.StatusCreated, "ok")
}

func setPreventSystemSleep(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureSleepHooks) {
		return
	}
	var p bool
	if err := c.BindJSON(&p); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf.SetPreventSystemSleep(p)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set prevent system sleep to %t", p)

	c.IndentedJSON(http.StatusCreated, "ok")
}

func setPreventSleepOnAdapterDisable(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureAdapterSleepPolicy) {
		return
	}
	var requested bool
	if err := c.BindJSON(&requested); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	chargeControlTransitionMu.Lock()
	defer chargeControlTransitionMu.Unlock()

	adapterPolicyMu.Lock()
	defer adapterPolicyMu.Unlock()

	adapterEnabled, err := smcIsAdapterEnabled()
	if err != nil {
		logrus.WithError(err).Error("failed to check adapter state for prevent-sleep setting")
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	current := conf.PreventSleepOnAdapterDisable()

	if requested {
		if !adapterEnabled {
			if err := holdSleep(sleepHoldAdapter); err != nil {
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
				_ = releaseSleep(sleepHoldAdapter)
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
		if err := releaseSleep(sleepHoldAdapter); err != nil {
			logrus.WithError(err).Error("failed to release sleep hold after disabling setting")
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	}

	logrus.Infof("set prevent sleep on adapter disable to %t", requested)

	c.IndentedJSON(http.StatusCreated, "ok")
}

func setAdapter(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureAdapterControl) {
		return
	}
	var d bool
	if err := c.BindJSON(&d); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	chargeControlTransitionMu.Lock()
	defer chargeControlTransitionMu.Unlock()

	if calibrationOwnsChargeLimit() {
		err := ErrCalibrationControlsAdapter
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	// Cutting power stops a one-time charge from making any progress, and an
	// indefinite adapter disable records no deadline that could resume it.
	if !d && conf.ChargeOnceTarget() != 0 {
		err := ErrChargeOnceInProgress
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if d {
		if err := smcEnableAdapter(); err != nil {
			logrus.Errorf("enablePowerAdapter failed: %v", err)
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		logrus.Infof("enabled power adapter")
	} else {
		if err := smcDisableAdapter(); err != nil {
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

func setAdapterDisableFor(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureAdapterControl) {
		return
	}
	var raw string
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	if d <= 0 {
		err := fmt.Errorf("duration must be positive, got %s", d)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	chargeControlTransitionMu.Lock()
	defer chargeControlTransitionMu.Unlock()

	if calibrationOwnsChargeLimit() {
		err := ErrCalibrationControlsAdapter
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	// Cutting power stops a one-time charge from making any progress.
	if conf.ChargeOnceTarget() != 0 {
		err := ErrChargeOnceInProgress
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	until := time.Now().Add(d).Truncate(time.Second)
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
	if err := smcDisableAdapter(); err != nil {
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

func getAdapter(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureAdapterControl) {
		return
	}
	enabled, err := smcIsAdapterEnabled()
	if err != nil {
		logrus.Errorf("getAdapter failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, enabled)
}

func getCharging(c *gin.Context) {
	caps := getCapabilities()
	if caps.ChargeControlMode != compatibility.ChargeControlLegacy {
		err := fmt.Errorf("direct charging state is not available in %s charge-control mode", caps.ChargeControlMode)
		c.IndentedJSON(http.StatusConflict, err.Error())
		_ = c.AbortWithError(http.StatusConflict, err)
		return
	}
	charging, err := smcConn.IsChargingEnabled()
	if err != nil {
		logrus.Errorf("getCharging failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, charging)
}

func getBatteryInfo(c *gin.Context) {
	// Use powerkit-go to retrieve current system info (IOKit only is sufficient here)
	info, err := powerkit.GetSystemInfo(powerkit.FetchOptions{QueryIOKit: true, QuerySMC: false})
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

	c.IndentedJSON(http.StatusOK, resp)
}

func setLowerLimitDelta(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureLowerLimit) {
		return
	}
	var d int
	if err := c.BindJSON(&d); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if d < 0 {
		err := fmt.Errorf("lower limit delta must be positive, got %d", d)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if conf.UpperLimit()-d < 10 {
		err := fmt.Errorf("lower limit delta must be less than limit - 10, got %d", d)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	conf.SetLowerLimit(conf.UpperLimit() - d)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	ret := fmt.Sprintf("set lower limit delta to %d, current upper/lower limit is %d%%/%d%%", d, conf.UpperLimit(), conf.LowerLimit())
	logrus.Info(ret)
	maintainLoopForced()

	c.IndentedJSON(http.StatusCreated, ret)
}

func setControlMagSafeLED(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureMagSafeLED) {
		return
	}
	// Check if MasSafe is supported first. If not, return error.
	if !smcConn.CheckMagSafeExistence() {
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

	conf.SetControlMagSafeLED(mode)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set control MagSafe LED to %s", mode)

	c.IndentedJSON(http.StatusCreated, fmt.Sprintf("ControlMagSafeLED set to %s. You should be able to see the effect in a few minutes.", mode))
}

func getCurrentCharge(c *gin.Context) {
	charge, err := smcConn.GetBatteryCharge()
	if err != nil {
		logrus.Errorf("getCurrentCharge failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, charge)
}

func getPluggedIn(c *gin.Context) {
	pluggedIn, err := smcConn.IsPluggedIn()
	if err != nil {
		logrus.Errorf("getCurrentCharge failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.IndentedJSON(http.StatusOK, pluggedIn)
}

func getChargingControlCapable(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, getCapabilities().ChargingControl)
}

func setAdapterMode(c *gin.Context) {
	var enabled bool
	if err := c.BindJSON(&enabled); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		return
	}

	chargeControlTransitionMu.Lock()
	defer chargeControlTransitionMu.Unlock()

	previous := conf.AdapterMode()
	previousTarget := conf.ChargeOnceTarget()
	conf.SetAdapterMode(enabled)
	// Native macOS cannot force a one-time target below 100%. Cancel such an
	// adapter-mode target in the same config update as the mode change.
	clearTarget := previousTarget > 0 && previousTarget < 100 &&
		getCapabilities().ChargeControlMode == compatibility.ChargeControlAdapter &&
		detectCapabilities().ChargeControlMode == compatibility.ChargeControlNative
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
	if err := reapplyChargeControlMode(); err != nil {
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
		reportSupersededChargeOnce(previousTarget, "adapter mode disabled")
	}
	c.IndentedJSON(http.StatusCreated, fmt.Sprintf("adapter mode set to %t, charge control is now %s", enabled, getCapabilities().ChargeControlMode))
}

func getVersion(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, version.Version)
}

func getPowerTelemetry(c *gin.Context) {
	// Use powerkit-go to fetch a snapshot of system power state
	c.Header("X-Deprecated", "true")
	c.Header("X-Deprecation-Info", "Use /telemetry?power=1 instead; /power-telemetry will be removed in a future release")
	info, err := powerkit.GetSystemInfo(powerkit.FetchOptions{QueryIOKit: true, QuerySMC: false})
	if err != nil || info == nil || info.IOKit == nil {
		if err == nil {
			err = errors.New("failed to fetch IOKit power data")
		}
		logrus.Errorf("getPowerTelemetry failed: %v", err)
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Build simplified telemetry expected by the GUI
	var snapshot powerinfo.PowerTelemetry
	snapshot.Adapter.InputVoltage = info.IOKit.Adapter.InputVoltage
	snapshot.Adapter.InputAmperage = info.IOKit.Adapter.InputAmperage

	snapshot.Battery.CycleCount = info.IOKit.Battery.CycleCount

	snapshot.Calculations.ACPower = info.IOKit.Calculations.AdapterPower
	snapshot.Calculations.BatteryPower = info.IOKit.Calculations.BatteryPower
	snapshot.Calculations.SystemPower = info.IOKit.Calculations.SystemPower
	snapshot.Calculations.HealthByMaxCapacity = info.IOKit.Calculations.HealthByMaxCapacity

	c.IndentedJSON(http.StatusOK, snapshot)
}

// Unified telemetry endpoint: /telemetry?power=1&calibration=1 (flags optional; default all)
func getUnifiedTelemetry(c *gin.Context) {
	wantPower := c.Query("power") != "0"
	wantCal := c.Query("calibration") != "0"

	resp := gin.H{}

	if wantPower {
		info, err := powerkit.GetSystemInfo(powerkit.FetchOptions{QueryIOKit: true, QuerySMC: false})
		if err != nil || info == nil || info.IOKit == nil {
			if err == nil {
				err = errors.New("failed to fetch IOKit power data")
			}
			logrus.WithError(err).Warn("power telemetry unavailable for unified telemetry")
		} else {
			var snapshot powerinfo.PowerTelemetry
			snapshot.Adapter.InputVoltage = info.IOKit.Adapter.InputVoltage
			snapshot.Adapter.InputAmperage = info.IOKit.Adapter.InputAmperage
			snapshot.Battery.CycleCount = info.IOKit.Battery.CycleCount
			snapshot.Calculations.ACPower = info.IOKit.Calculations.AdapterPower
			snapshot.Calculations.BatteryPower = info.IOKit.Calculations.BatteryPower
			snapshot.Calculations.SystemPower = info.IOKit.Calculations.SystemPower
			snapshot.Calculations.HealthByMaxCapacity = info.IOKit.Calculations.HealthByMaxCapacity
			resp["power"] = snapshot
		}
	}

	if wantCal && getCapabilities().Calibration {
		resp["calibration"] = getCalibrationStatus()
	}

	// Add deprecation header if caller still hitting legacy endpoints (not detectable here), but we can add a generic hint.
	c.Header("X-Batt-Telemetry-Version", "1")
	c.IndentedJSON(http.StatusOK, resp)
}

// SSE endpoint: streams daemon events (first: calibration phase changes)
func getEventStream(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.Status(http.StatusInternalServerError)
		return
	}

	ch := sseHub.Subscribe()
	defer sseHub.Unsubscribe(ch)

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

func postChargeOnceToLimit(c *gin.Context) { startChargeOnceRequest(c, false) }

func postChargeOnceToFull(c *gin.Context) { startChargeOnceRequest(c, true) }

func startChargeOnceRequest(c *gin.Context, full bool) {
	if !requireCapability(c, compatibility.FeatureChargingControl) {
		return
	}

	chargeControlTransitionMu.Lock()
	defer chargeControlTransitionMu.Unlock()

	// Conflicts come first: a pending temporary disable also reads as a
	// disabled charge limit, and naming the conflict tells the user what to do.
	if err := chargeOnceConflict(conf); err != nil {
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

	target, err := resolveChargeOnceTarget(conf, full)
	if err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	mode := getCapabilities().ChargeControlMode
	if !full && mode == compatibility.ChargeControlNative {
		c.IndentedJSON(http.StatusConflict, ErrNativeChargeNow.Error())
		_ = c.AbortWithError(http.StatusConflict, ErrNativeChargeNow)
		return
	}

	charge, err := smcGetBatteryCharge()
	if err != nil {
		logrus.Errorf("GetBatteryCharge failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Admission asks the same question completion does, so a charge the next
	// maintain loop would end right away is refused instead of started.
	if chargeOnceReachedTarget(target, charge) {
		err := fmt.Errorf("battery is already at %d%%, so a one-time charge to %d%% would end immediately", charge, target)
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	// Adapter mode must not inherit a native macOS limit from a previous mode
	// or reboot. Remember the previous value in case admission fails.
	var nativePreviousLimit int
	var nativePreviouslyEnabled bool
	if mode == compatibility.ChargeControlAdapter && nativeLimit.Supported() {
		var err error
		nativePreviousLimit, nativePreviouslyEnabled, err = nativeLimit.Limit()
		if err == nil {
			_, err = ensureNativeChargeLimitDisabled()
		}
		if err != nil {
			c.IndentedJSON(http.StatusInternalServerError, err.Error())
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	}
	restoreNativeLimit := func() error {
		if nativePreviouslyEnabled {
			return nativeLimit.SetLimit(nativePreviousLimit)
		}
		return nil
	}

	message := chargeOnceStartedMessage(target, charge, conf.UpperLimit())
	if err := startChargeOnce(target, charge); err != nil {
		if restoreErr := restoreNativeLimit(); restoreErr != nil {
			err = fmt.Errorf("%w; failed to restore native limit: %v", err, restoreErr)
		}
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// A successful API response requires the first enforcement pass to work.
	if !maintainLoopForced() {
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
	reportChargeOnceStarted(target, charge)
	c.IndentedJSON(http.StatusCreated, message)
}

func postCancelChargeOnce(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureChargingControl) {
		return
	}

	chargeControlTransitionMu.Lock()
	defer chargeControlTransitionMu.Unlock()

	target, err := cancelChargeOnce()
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

	maintainLoopForced()

	c.IndentedJSON(http.StatusOK, fmt.Sprintf("cancelled the one-time charge to %d%%, the %d%% charge limit applies again", target, conf.UpperLimit()))
}

// ===== Calibration Handlers =====

func postStartCalibration(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	// Read threshold & hold from current config getters
	threshold := conf.CalibrationDischargeThreshold()
	hold := conf.CalibrationHoldDurationMinutes()
	if err := startCalibration(threshold, hold); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	c.IndentedJSON(http.StatusCreated, gin.H{"ok": true})
}

func postPauseCalibration(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	if err := pauseCalibration(); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func postResumeCalibration(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	if err := resumeCalibration(); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func postCancelCalibration(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	if err := cancelCalibration(); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func setSchedule(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	var cronExpr string
	if err := c.BindJSON(&cronExpr); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	nextRuns, err := schedule(cronExpr)
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

func skipSchedule(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureCalibration) {
		return
	}
	if err := skipNextSchedule(); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func postponeSchedule(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureCalibration) {
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

	d, err := time.ParseDuration(raw)
	if err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if err := postpone(d); err != nil {
		c.IndentedJSON(http.StatusBadRequest, err.Error())
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	c.IndentedJSON(http.StatusOK, gin.H{"ok": true})
}

func setCalibrationDischargeThreshold(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureCalibration) {
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

	conf.SetCalibrationDischargeThreshold(threshold)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set calibration discharge threshold to %d", threshold)

	// Check if calibration is running
	st := getCalibrationStatus()
	msg := fmt.Sprintf("Calibration discharge threshold set to %d%%", threshold)
	if st.Phase != calibration.PhaseIdle && st.Phase != calibration.PhaseRestore && st.Phase != calibration.PhaseError {
		msg += ". Note: A calibration is currently in progress. The new threshold will take effect on the next calibration."
	}

	c.IndentedJSON(http.StatusCreated, msg)
}

func setCalibrationHoldDurationMinutes(c *gin.Context) {
	if !requireCapability(c, compatibility.FeatureCalibration) {
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

	conf.SetCalibrationHoldDurationMinutes(minutes)
	if err := conf.Save(); err != nil {
		logrus.Errorf("saveConfig failed: %v", err)
		c.IndentedJSON(http.StatusInternalServerError, err.Error())
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	logrus.Infof("set calibration hold duration to %d minutes", minutes)

	// Check if calibration is running
	st := getCalibrationStatus()
	msg := fmt.Sprintf("Calibration hold duration set to %d minutes", minutes)
	if st.Phase != calibration.PhaseIdle && st.Phase != calibration.PhaseRestore && st.Phase != calibration.PhaseError {
		msg += ". Note: A calibration is currently in progress. The new hold duration will take effect on the next calibration."
	}

	c.IndentedJSON(http.StatusCreated, msg)
}
