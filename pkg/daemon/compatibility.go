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
	legacy := mode == compatibility.ChargeControlLegacy
	adapter := smcConn.IsAdapterControlCapable()
	return compatibility.Capabilities{
		ChargingControl:   mode != compatibility.ChargeControlUnsupported,
		ChargeControlMode: mode,
		SleepHooks:        legacy,
		// LED state follows batt's direct charging state, which is not
		// available when the firmware owns charge control.
		MagSafeLED:     legacy && smcConn.CheckMagSafeExistence(),
		AdapterControl: adapter,
		// The calibration workflow requires both direct charge and adapter
		// control to perform its discharge phases.
		Calibration: legacy && adapter,
	}
}

func capabilityLogFields(capabilities compatibility.Capabilities) logrus.Fields {
	return logrus.Fields{
		"chargingControl":   capabilities.ChargingControl,
		"chargeControlMode": capabilities.ChargeControlMode,
		"sleepHooks":        capabilities.SleepHooks,
		"magSafeLED":        capabilities.MagSafeLED,
		"adapterControl":    capabilities.AdapterControl,
		"calibration":       capabilities.Calibration,
	}
}

func disableUnsupportedCalibrationState() {
	if capabilities.Calibration {
		return
	}
	calibrationMu.Lock()
	defer calibrationMu.Unlock()
	if calibrationState.Phase == calibration.PhaseIdle {
		return
	}
	logrus.WithField("phase", calibrationState.Phase).Info("discarding unsupported persisted calibration state")
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
	changed := false
	if !capabilities.SleepHooks {
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
	if !capabilities.MagSafeLED && conf.ControlMagSafeLED() != config.ControlMagSafeModeDisabled {
		conf.SetControlMagSafeLED(config.ControlMagSafeModeDisabled)
		changed = true
	}
	if !capabilities.Calibration && conf.Cron() != "" {
		conf.SetCron("")
		changed = true
	}
	if !changed {
		return
	}
	if err := conf.Save(); err != nil {
		logrus.WithError(err).Error("failed to persist disabled unsupported features")
		return
	}
	logrus.WithFields(capabilityLogFields(capabilities)).Info("disabled unsupported configured features")
}

func requireCapability(c *gin.Context, feature compatibility.Feature) bool {
	if capabilities.Supports(feature) {
		return true
	}
	err := fmt.Errorf("%s is not supported on this Mac", feature)
	c.IndentedJSON(http.StatusConflict, err.Error())
	_ = c.AbortWithError(http.StatusConflict, err)
	return false
}

func getCompatibility(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, capabilities)
}
