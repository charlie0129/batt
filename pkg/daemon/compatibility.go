package daemon

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
)

func detectCapabilities() compatibility.Capabilities {
	mode := smcConn.ChargeControlMode()
	adapter := smcConn.IsAdapterControlCapable()
	var supportedLimits []int
	if mode == compatibility.ChargeControlUnsupported {
		// Adapter mode is opt-in; otherwise fall back to the native limit.
		switch {
		case adapter && conf.AdapterMode():
			mode = compatibility.ChargeControlAdapter
		default:
			mode, supportedLimits = detectNativeChargeControl()
		}
	}
	legacy := mode == compatibility.ChargeControlLegacy
	active := legacy || mode == compatibility.ChargeControlAdapter
	return compatibility.Capabilities{
		ChargingControl:   mode != compatibility.ChargeControlUnsupported,
		ChargeControlMode: mode,
		SupportedLimits:   supportedLimits,
		// The legacy and adapter loops both run while awake and rely on the
		// sleep hooks to avoid overcharging during sleep.
		SleepHooks: active,
		// LED state follows batt's direct charging state, which is only known
		// when batt owns the charge-enable keys.
		MagSafeLED: legacy && smcConn.CheckMagSafeExistence(),
		// In adapter mode batt owns the adapter to enforce the limit, so the
		// manual adapter/force-discharge feature is hidden to avoid conflicts.
		AdapterControl: adapter && mode != compatibility.ChargeControlAdapter,
		// Calibration drives the adapter through discharge phases. It is not
		// yet wired for adapter mode, which already owns the adapter.
		Calibration: mode != compatibility.ChargeControlUnsupported &&
			mode != compatibility.ChargeControlAdapter && adapter,
	}
}

// reapplyChargeControlMode re-detects capabilities after the adapter-mode
// setting changed, restores wall power when leaving adapter mode, and enforces
// the new mode immediately.
func reapplyChargeControlMode() error {
	maintainLoopInnerLock.Lock()
	previous, previousCharger := loadChargeControl()
	prev := previous.ChargeControlMode
	next := detectCapabilities()
	if prev == compatibility.ChargeControlNative && next.ChargeControlMode == compatibility.ChargeControlAdapter {
		if _, err := ensureNativeChargeLimitDisabled(); err != nil {
			maintainLoopInnerLock.Unlock()
			return fmt.Errorf("failed to disable native limit before entering adapter mode: %w", err)
		}
	}
	if prev == compatibility.ChargeControlAdapter && next.ChargeControlMode != compatibility.ChargeControlAdapter {
		if err := smcEnableAdapter(); err != nil {
			maintainLoopInnerLock.Unlock()
			return fmt.Errorf("failed to restore adapter when leaving adapter mode: %w", err)
		}
	}
	if err := reconcileAdapterSleepPolicy(); err != nil {
		maintainLoopInnerLock.Unlock()
		return restoreAdapterAfterPolicyError(err)
	}
	storeChargeControl(next, selectCharger(next.ChargeControlMode))
	maintainLoopInnerLock.Unlock()

	if !maintainLoopForced() {
		maintainLoopInnerLock.Lock()
		storeChargeControl(previous, previousCharger)
		maintainLoopInnerLock.Unlock()
		if !maintainLoopForced() {
			return fmt.Errorf("failed to enforce %s charge-control mode; previous %s mode could not be restored either", next.ChargeControlMode, prev)
		}
		return fmt.Errorf("failed to enforce %s charge-control mode; previous %s mode restored", next.ChargeControlMode, prev)
	}

	logrus.WithFields(capabilityLogFields(next)).Info("reapplied charge control mode")
	disableUnsupportedConfiguredFeatures()
	return nil
}

// detectNativeChargeControl falls back to the charge limit built into macOS
// when the SMC keys are gated (macOS 27 beta 4+ firmware). It only reports the
// native mode when PowerUIAgent supports the limit and lists usable values.
func detectNativeChargeControl() (compatibility.ChargeControlMode, []int) {
	if !nativeLimit.Supported() {
		return compatibility.ChargeControlUnsupported, nil
	}
	limits, err := nativeLimit.AvailableLimits()
	if err != nil {
		logrus.WithError(err).Warn("macOS reports a manual charge limit but its supported values could not be read")
		return compatibility.ChargeControlUnsupported, nil
	}
	if len(limits) == 0 {
		logrus.Warn("macOS reports a manual charge limit but offers no supported values")
		return compatibility.ChargeControlUnsupported, nil
	}
	return compatibility.ChargeControlNative, limits
}

func capabilityLogFields(capabilities compatibility.Capabilities) logrus.Fields {
	return logrus.Fields{
		"chargingControl":   capabilities.ChargingControl,
		"chargeControlMode": capabilities.ChargeControlMode,
		"supportedLimits":   capabilities.SupportedLimits,
		"sleepHooks":        capabilities.SleepHooks,
		"magSafeLED":        capabilities.MagSafeLED,
		"adapterControl":    capabilities.AdapterControl,
		"calibration":       capabilities.Calibration,
	}
}

func disableUnsupportedCalibrationState() {
	if getCapabilities().Calibration {
		return
	}
	calibrationMu.Lock()
	defer calibrationMu.Unlock()
	if calibrationState.Phase == calibration.PhaseIdle {
		return
	}
	logrus.WithField("phase", calibrationState.Phase).Info("discarding unsupported persisted calibration state")
	releaseCalibrationSleepAssertion()
	// Calibration may have temporarily changed the configured limit to 100%.
	// Restore its saved limits without touching unsupported charging/adapter
	// keys before discarding the workflow state.
	if calibrationState.SnapshotUpperLimit >= 10 && calibrationState.SnapshotUpperLimit <= 100 &&
		calibrationState.SnapshotLowerLimit >= 0 && calibrationState.SnapshotLowerLimit < calibrationState.SnapshotUpperLimit {
		conf.SetUpperLimit(calibrationState.SnapshotUpperLimit)
		conf.SetLowerLimit(calibrationState.SnapshotLowerLimit)
		if err := conf.Save(); err != nil {
			logrus.WithError(err).Error("failed to restore limits from unsupported calibration state")
		}
	}
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	persistCalibrationState()
}

// disableUnsupportedConfiguredFeatures prevents settings left behind by an
// OS/firmware upgrade from activating features that are unsafe on the current
// hardware. It intentionally persists the disabled values.
func disableUnsupportedConfiguredFeatures() {
	caps := getCapabilities()
	changed := false
	if !caps.SleepHooks {
		if conf.PreventIdleSleep() {
			conf.SetPreventIdleSleep(false)
			changed = true
		}
		if conf.DisableChargingPreSleep() {
			conf.SetDisableChargingPreSleep(false)
			changed = true
		}
		if conf.PreventSystemSleep() {
			conf.SetPreventSystemSleep(false)
			changed = true
		}
	}
	if !caps.MagSafeLED && conf.ControlMagSafeLED() != config.ControlMagSafeModeDisabled {
		conf.SetControlMagSafeLED(config.ControlMagSafeModeDisabled)
		changed = true
	}
	if !caps.Calibration && conf.Cron() != "" {
		conf.SetCron("")
		changed = true
	}
	if !caps.AdapterControl && !conf.AdapterDisableUntil().IsZero() {
		conf.ClearAdapterDisableTimer()
		changed = true
	}
	// A limit configured before an upgrade may not be one macOS offers. Raise
	// it to the next supported value rather than charging past it.
	if upper := conf.UpperLimit(); !caps.SupportsLimit(upper) {
		snapped := caps.NearestSupportedLimit(upper)
		logrus.WithFields(logrus.Fields{
			"configured":      upper,
			"limit":           snapped,
			"supportedLimits": caps.SupportedLimits,
		}).Warn("configured charge limit is not offered by this Mac, raising it to the next supported limit")
		conf.SetUpperLimit(snapped)
		changed = true
	}
	if target := conf.ChargeOnceTarget(); target != 0 && (!caps.ChargingControl ||
		(caps.ChargeControlMode == compatibility.ChargeControlNative && target < 100)) {
		conf.ClearChargeOnceTarget()
		changed = true
	}
	if !changed {
		return
	}
	if err := conf.Save(); err != nil {
		logrus.WithError(err).Error("failed to persist disabled unsupported features")
		return
	}
	logrus.WithFields(capabilityLogFields(caps)).Info("disabled unsupported configured features")
}

func requireCapability(c *gin.Context, feature compatibility.Feature) bool {
	if getCapabilities().Supports(feature) {
		return true
	}
	err := fmt.Errorf("%s is not supported on this Mac", feature)
	c.IndentedJSON(http.StatusConflict, err.Error())
	_ = c.AbortWithError(http.StatusConflict, err)
	return false
}

func getCompatibility(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, getCapabilities())
}
