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

func (d *Daemon) detectCapabilities() compatibility.Capabilities {
	mode := d.smc.ChargeControlMode()
	adapter := d.smc.IsAdapterControlCapable()
	var supportedLimits []int
	if mode == compatibility.ChargeControlUnsupported {
		// Adapter mode is opt-in; otherwise fall back to the native limit.
		switch {
		case adapter && d.conf.AdapterMode():
			mode = compatibility.ChargeControlAdapter
		default:
			mode, supportedLimits = d.detectNativeChargeControl()
		}
	}
	legacy := mode == compatibility.ChargeControlLegacy
	active := legacy || mode == compatibility.ChargeControlAdapter
	managed := mode == compatibility.ChargeControlFirmware || mode == compatibility.ChargeControlNative
	return compatibility.Capabilities{
		ChargingControl:    mode != compatibility.ChargeControlUnsupported,
		ChargeControlMode:  mode,
		NativeExactTargets: mode == compatibility.ChargeControlNative && d.native.SupportsExactTargets(),
		SupportedLimits:    supportedLimits,
		// The legacy and adapter loops both run while awake and rely on the
		// sleep hooks to avoid overcharging during sleep.
		SleepHooks: active,
		// Active modes use batt's switch state; firmware and native modes use
		// the actual charging state reported by macOS.
		MagSafeLED: (active || managed) && d.smc.CheckMagSafeExistence(),
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
func (d *Daemon) reapplyChargeControlMode() error {
	d.loopMu.Lock()
	previous, previousCharger := d.loadChargeControl()
	prev := previous.ChargeControlMode
	next := d.detectCapabilities()
	if prev == compatibility.ChargeControlNative && next.ChargeControlMode == compatibility.ChargeControlAdapter {
		if _, err := d.ensureNativeChargeLimitDisabled(); err != nil {
			d.loopMu.Unlock()
			return fmt.Errorf("failed to disable native limit before entering adapter mode: %w", err)
		}
	}
	if prev == compatibility.ChargeControlAdapter && next.ChargeControlMode != compatibility.ChargeControlAdapter {
		if err := d.enableAdapter(); err != nil {
			d.loopMu.Unlock()
			return fmt.Errorf("failed to restore adapter when leaving adapter mode: %w", err)
		}
	}
	if err := d.reconcileAdapterSleepPolicy(); err != nil {
		d.loopMu.Unlock()
		return d.restoreAdapterAfterPolicyError(err)
	}
	d.storeChargeControl(next, d.selectCharger(next.ChargeControlMode))
	d.loopMu.Unlock()

	if !d.maintainLoopForced() {
		d.loopMu.Lock()
		d.storeChargeControl(previous, previousCharger)
		d.loopMu.Unlock()
		if !d.maintainLoopForced() {
			return fmt.Errorf("failed to enforce %s charge-control mode; previous %s mode could not be restored either", next.ChargeControlMode, prev)
		}
		return fmt.Errorf("failed to enforce %s charge-control mode; previous %s mode restored", next.ChargeControlMode, prev)
	}

	logrus.WithFields(capabilityLogFields(next)).Info("reapplied charge control mode")
	d.disableUnsupportedConfiguredFeatures()
	return nil
}

// detectNativeChargeControl falls back to the charge limit built into macOS
// when the SMC keys are gated (macOS 27 beta 4+ firmware). It only reports the
// native mode when PowerUIAgent supports the limit and lists usable values.
func (d *Daemon) detectNativeChargeControl() (compatibility.ChargeControlMode, []int) {
	if !d.native.Supported() {
		return compatibility.ChargeControlUnsupported, nil
	}
	limits, err := d.native.AvailableLimits()
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
		"chargingControl":    capabilities.ChargingControl,
		"chargeControlMode":  capabilities.ChargeControlMode,
		"nativeExactTargets": capabilities.NativeExactTargets,
		"supportedLimits":    capabilities.SupportedLimits,
		"sleepHooks":         capabilities.SleepHooks,
		"magSafeLED":         capabilities.MagSafeLED,
		"adapterControl":     capabilities.AdapterControl,
		"calibration":        capabilities.Calibration,
	}
}

func (d *Daemon) disableUnsupportedCalibrationState() {
	if d.getCapabilities().Calibration {
		return
	}
	d.calibrationMu.Lock()
	defer d.calibrationMu.Unlock()
	if d.calibrationState.Phase == calibration.PhaseIdle {
		return
	}
	logrus.WithField("phase", d.calibrationState.Phase).Info("discarding unsupported persisted calibration state")
	d.releaseCalibrationSleepAssertion()
	// Calibration may have temporarily changed the configured limit to 100%.
	// Restore its saved limits without touching unsupported charging/adapter
	// keys before discarding the workflow state.
	st := d.calibrationState
	if st.SnapshotUpperLimit >= 10 && st.SnapshotUpperLimit <= 100 &&
		st.SnapshotLowerLimit >= 0 && st.SnapshotLowerLimit < st.SnapshotUpperLimit {
		d.conf.SetUpperLimit(st.SnapshotUpperLimit)
		d.conf.SetLowerLimit(st.SnapshotLowerLimit)
		if err := d.conf.Save(); err != nil {
			logrus.WithError(err).Error("failed to restore limits from unsupported calibration state")
		}
	}
	d.calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	d.persistCalibrationState()
}

// disableUnsupportedConfiguredFeatures prevents settings left behind by an
// OS/firmware upgrade from activating features that are unsafe on the current
// hardware. It intentionally persists the disabled values.
func (d *Daemon) disableUnsupportedConfiguredFeatures() {
	caps := d.getCapabilities()
	conf := d.conf
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

func (d *Daemon) requireCapability(c *gin.Context, feature compatibility.Feature) bool {
	if d.getCapabilities().Supports(feature) {
		return true
	}
	err := fmt.Errorf("%s is not supported on this Mac", feature)
	c.IndentedJSON(http.StatusConflict, err.Error())
	_ = c.AbortWithError(http.StatusConflict, err)
	return false
}

func (d *Daemon) getCompatibility(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, d.getCapabilities())
}
