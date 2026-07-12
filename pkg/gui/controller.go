package gui

import (
	"context"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/client"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
	"github.com/charlie0129/batt/pkg/version"
)

type menuController struct {
	api  *client.Client
	menu *nativeMenu

	calibrationThreshold int
	eventCancel          context.CancelFunc
}

func (c *menuController) onWillOpen() {
	c.refreshOnOpen()
	c.updateTelemetry()
}

func (c *menuController) onTimerTick() {
	c.updateTelemetry()
}

func (c *menuController) refreshCompatibility() {
	logrus.Info("Getting config")
	rawConfig, err := c.api.GetConfig()
	if err != nil {
		logrus.WithError(err).Warn("Failed to get config")
		c.setCompatibility(false, false, false)
		return
	}
	conf := config.NewFileFromConfig(rawConfig, "")
	logrus.WithFields(conf.LogrusFields()).Info("Got config")

	logrus.Info("Getting charging control capability")
	capable, err := c.api.GetChargingControlCapable()
	if err != nil {
		logrus.WithError(err).Warn("Failed to get charging capablility")
		c.setCompatibility(true, false, false)
		return
	}
	logrus.WithField("capable", capable).Info("Got charging control capability")

	logrus.Info("Getting daemon version")
	daemonVersion, err := c.api.GetVersion()
	if err != nil {
		logrus.WithError(err).Warn("Failed to get version")
		c.setCompatibility(true, capable, true)
	} else {
		c.setCompatibility(true, capable, daemonVersion != version.Version)
	}
	logrus.WithFields(logrus.Fields{
		"daemonVersion": daemonVersion,
		"clientVersion": version.Version,
	}).Info("Got daemon")
}

func (c *menuController) refreshOnOpen() {
	rawConfig, err := c.api.GetConfig()
	if err != nil {
		logrus.WithError(err).Error("Failed to get config")
		c.setCompatibility(false, false, false)
		return
	}
	capable, err := c.api.GetChargingControlCapable()
	if err != nil {
		logrus.WithError(err).Error("Failed to get charging capablility")
		c.setCompatibility(true, false, false)
		return
	}
	daemonVersion, err := c.api.GetVersion()
	if err != nil {
		logrus.WithError(err).Error("Failed to get version")
		c.setCompatibility(true, capable, true)
	} else {
		c.setCompatibility(true, capable, daemonVersion != version.Version)
	}
	logrus.WithFields(logrus.Fields{
		"daemonVersion": daemonVersion,
		"clientVersion": version.Version,
	}).Info("Got daemon")

	isCharging, err := c.api.GetCharging()
	if err != nil {
		c.setStateError("Failed to get charging state", err)
		return
	}
	isPluggedIn, err := c.api.GetPluggedIn()
	if err != nil {
		c.setStateError("Failed to get plugged in state", err)
		return
	}
	currentCharge, err := c.api.GetCurrentCharge()
	if err != nil {
		c.setStateError("Failed to get current charge", err)
		return
	}
	batteryInfo, err := c.api.GetBatteryInfo()
	if err != nil {
		c.setStateError("Failed to get battery info", err)
		return
	}

	conf := config.NewFileFromConfig(rawConfig, "")
	logrus.WithFields(conf.LogrusFields()).Info("Got config")
	c.calibrationThreshold = conf.CalibrationDischargeThreshold()
	c.menu.setTitle(itemCurrentLimit, fmt.Sprintf("Current Limit: %d%%", conf.UpperLimit()))
	for _, item := range quickLimitItems {
		c.menu.setChecked(item, quickLimitForItem(item) == conf.UpperLimit())
	}

	state := "Not Charging"
	switch batteryInfo.State {
	case powerinfo.Charging:
		state = "Charging"
	case powerinfo.Discharging:
		if batteryInfo.ChargeRate != 0 {
			state = "Discharging"
		}
	case powerinfo.Full:
		state = "Full"
	}
	if !isCharging && isPluggedIn && conf.UpperLimit() < 100 && currentCharge < conf.LowerLimit() {
		state = "Will Charge Soon"
	}
	c.menu.setTitle(itemState, "State: "+state)

	c.updateMagSafeChecks(conf.ControlMagSafeLED())
	c.menu.setChecked(itemPreventIdleSleep, conf.PreventIdleSleep())
	c.menu.setChecked(itemDisableChargingPreSleep, conf.DisableChargingPreSleep())
	c.menu.setChecked(itemPreventSystemSleep, conf.PreventSystemSleep())
	if adapter, err := c.api.GetAdapter(); err == nil {
		c.menu.setChecked(itemForceDischarge, !adapter)
	} else {
		logrus.WithError(err).Error("Failed to get adapter")
		c.menu.setEnabled(itemForceDischarge, false)
	}
}

func (c *menuController) setStateError(message string, err error) {
	logrus.WithError(err).Error(message)
	c.menu.setTitle(itemState, "State: Error")
}

func (c *menuController) setCompatibility(installed, capable, needsUpgrade bool) {
	if value := os.Getenv("BATT_GUI_NO_COMPATIBILITY_CHECK"); value == "1" || value == "true" {
		return
	}
	c.menu.setStatusIcon(installed, capable, needsUpgrade)

	usable := installed && capable && !needsUpgrade
	c.menu.setHidden(itemPowerFlow, !usable)
	c.menu.setHidden(itemInstall, installed)
	c.menu.setHidden(itemUpgrade, !installed || (!needsUpgrade && capable))
	c.menu.setHidden(itemState, !installed || !capable)
	c.menu.setHidden(itemCurrentLimit, !installed || !capable)
	c.menu.setHidden(itemQuickLimits, !usable)
	for _, item := range quickLimitItems {
		c.menu.setHidden(item, !usable)
	}

	c.menu.setHidden(itemAdvanced, !installed)
	for _, item := range []menuItem{
		itemMagSafe,
		itemPreventIdleSleep,
		itemDisableChargingPreSleep,
		itemPreventSystemSleep,
		itemForceDischarge,
		itemAutoCalibration,
	} {
		c.menu.setHidden(item, !usable)
	}
	c.menu.setHidden(itemUninstall, !installed)
	c.menu.setHidden(itemDisableLimit, !usable)
	if installed {
		c.menu.setTooltip(itemQuit, quitTooltipInstalled)
	} else {
		c.menu.setTooltip(itemQuit, quitTooltipNotInstalled)
	}
}

func (c *menuController) updateTelemetry() {
	telemetry, err := c.api.GetTelemetry(true, true)
	if err != nil || telemetry == nil {
		if err != nil {
			logrus.WithError(err).Debug("GetTelemetry failed")
		}
		return
	}
	if telemetry.Power != nil {
		calculations := telemetry.Power.Calculations
		c.menu.setPower(itemPowerSystem, "System", calculations.SystemPower)
		c.menu.setPower(itemPowerAdapter, "Adapter", calculations.ACPower)
		c.menu.setPower(itemPowerBattery, "Battery", calculations.BatteryPower)
	}
	if telemetry.Calibration != nil {
		c.updateCalibration(telemetry.Calibration)
	}
}

func (c *menuController) updateCalibration(status *calibration.Status) {
	isIdle := status.Phase == calibration.PhaseIdle
	switch {
	case isIdle:
		c.menu.setTitle(itemAutoCalibration, "Auto Calibration (Experimental)...")
	case status.Paused:
		c.menu.setTitle(itemAutoCalibration, "Auto Calibration (Experimental) Paused...")
	default:
		c.menu.setTitle(itemAutoCalibration, "Auto Calibration (Experimental) In Progress...")
	}

	c.menu.setEnabled(itemCalibrationStart, isIdle)
	c.menu.setEnabled(itemCalibrationCancel, !isIdle)
	c.menu.setEnabled(itemCalibrationPause, !isIdle && !status.Paused)
	c.menu.setEnabled(itemCalibrationResume, status.Paused)
	if title, ok := c.calibrationStatusTitle(status); ok {
		c.menu.setTitle(itemCalibrationStatus, title)
	}

	settingsEnabled := isIdle || status.Phase == calibration.PhaseError || status.Paused
	for _, item := range append([]menuItem{
		itemForceDischarge,
		itemUninstall,
		itemDisableLimit,
	}, quickLimitItems...) {
		c.menu.setEnabled(item, settingsEnabled)
	}
}

func (c *menuController) calibrationStatusTitle(status *calibration.Status) (string, bool) {
	switch status.Phase {
	case calibration.PhaseIdle:
		return "Status: Idle", true
	case calibration.PhaseDischarge:
		return fmt.Sprintf("Status: Discharging (%d%% → %d%%)", status.ChargePercent, c.calibrationThreshold), true
	case calibration.PhaseCharge:
		return fmt.Sprintf("Status: Charging (%d%% → 100%%)", status.ChargePercent), true
	case calibration.PhaseHold:
		hours := status.RemainingHoldSecs / 3600
		minutes := (status.RemainingHoldSecs % 3600) / 60
		seconds := status.RemainingHoldSecs % 60
		return fmt.Sprintf("Status: Holding (%02d:%02d:%02d left)", hours, minutes, seconds), true
	case calibration.PhasePostHold:
		if status.TargetPercent > 0 {
			return fmt.Sprintf("Status: Discharging (%d%% → %d%%)", status.ChargePercent, status.TargetPercent), true
		}
		return "Status: Discharging to previous limit...", true
	case calibration.PhaseRestore:
		return "Status: Restoring settings...", true
	case calibration.PhaseError:
		if status.Message != "" {
			return "Status: Error - " + status.Message, true
		}
		return "Status: Error", true
	default:
		return "", false
	}
}

func (c *menuController) updateMagSafeChecks(mode config.ControlMagSafeMode) {
	c.menu.setChecked(itemMagSafeEnabled, mode == config.ControlMagSafeModeEnabled)
	c.menu.setChecked(itemMagSafeAlwaysOff, mode == config.ControlMagSafeModeAlwaysOff)
	c.menu.setChecked(itemMagSafeDisabled, mode != config.ControlMagSafeModeEnabled && mode != config.ControlMagSafeModeAlwaysOff)
}
