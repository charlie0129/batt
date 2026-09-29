package gui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/client"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
	"github.com/charlie0129/batt/pkg/version"
)

type menuController struct {
	api  *client.Client
	menu *nativeMenu

	calibrationThreshold    int
	calibrationPhase        calibration.Phase
	capabilities            compatibility.Capabilities
	compatibilityKnown      bool
	upperLimit              int
	currentCharge           int
	chargeOnceTarget        int
	disableScheduled        bool
	adapterDisableScheduled bool
	adapterEnabled          bool
	adapterKnown            bool
	eventCancel             context.CancelFunc
}

func (c *menuController) onWillOpen() {
	c.refreshOnOpen()
	c.updateTelemetry()
}

func (c *menuController) onTimerTick() {
	c.refreshDisableSchedules()
	c.updateTelemetry()
}

func (c *menuController) refreshCompatibility() {
	logrus.Info("Getting config")
	rawConfig, err := c.api.GetConfig()
	if err != nil {
		logrus.WithError(err).Warn("Failed to get config")
		c.setCompatibility(false, compatibility.Permissive(), false)
		return
	}
	conf := config.NewFileFromConfig(rawConfig, "")
	logrus.WithFields(conf.LogrusFields()).Info("Got config")

	logrus.Info("Getting hardware compatibility")
	capabilities, err := c.api.GetCompatibility()
	c.compatibilityKnown = err == nil
	if err != nil {
		logrus.WithError(err).Warn("Detailed compatibility unavailable; enabling all GUI features")
		fallback := compatibility.Permissive()
		capabilities = &fallback
	}
	logrus.WithField("capabilities", capabilities).Info("Got hardware compatibility")

	logrus.Info("Getting daemon version")
	daemonVersion, err := c.api.GetVersion()
	if err != nil {
		logrus.WithError(err).Warn("Failed to get version")
		c.setCompatibility(true, *capabilities, true)
	} else {
		c.setCompatibility(true, *capabilities, daemonVersion != version.Version)
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
		c.setCompatibility(false, compatibility.Permissive(), false)
		return
	}
	conf := config.NewFileFromConfig(rawConfig, "")
	logrus.WithFields(conf.LogrusFields()).Info("Got config")
	c.updateDisableSchedules(conf)

	capabilities, err := c.api.GetCompatibility()
	c.compatibilityKnown = err == nil
	if err != nil {
		logrus.WithError(err).Warn("Detailed compatibility unavailable; enabling all GUI features")
		fallback := compatibility.Permissive()
		capabilities = &fallback
	}
	daemonVersion, err := c.api.GetVersion()
	if err != nil {
		logrus.WithError(err).Error("Failed to get version")
		c.setCompatibility(true, *capabilities, true)
	} else {
		c.setCompatibility(true, *capabilities, daemonVersion != version.Version)
	}
	logrus.WithFields(logrus.Fields{
		"daemonVersion": daemonVersion,
		"clientVersion": version.Version,
	}).Info("Got daemon")

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
	allowsCharging := batteryInfo.State == powerinfo.Charging
	if capabilities.ChargeControlMode == compatibility.ChargeControlLegacy {
		allowsCharging, err = c.api.GetCharging()
		if err != nil {
			c.setStateError("Failed to get charging state", err)
			return
		}
	}

	c.calibrationThreshold = conf.CalibrationDischargeThreshold()
	c.updateChargeOnce(conf, currentCharge)
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
	if capabilities.ChargeControlMode == compatibility.ChargeControlLegacy && !allowsCharging && isPluggedIn && conf.UpperLimit() < 100 && currentCharge < conf.LowerLimit() {
		state = "Will Charge Soon"
	}
	c.menu.setTitle(itemState, "State: "+state)

	c.updateMagSafeChecks(conf.ControlMagSafeLED())
	c.menu.setChecked(itemPreventIdleSleep, conf.PreventIdleSleep())
	c.menu.setChecked(itemDisableChargingPreSleep, conf.DisableChargingPreSleep())
	c.menu.setChecked(itemPreventSystemSleep, conf.PreventSystemSleep())
	if capabilities.AdapterControl {
		if adapter, err := c.api.GetAdapter(); err == nil {
			c.updateAdapterState(adapter)
		} else {
			logrus.WithError(err).Error("Failed to get adapter")
			c.adapterKnown = false
			if c.compatibilityKnown {
				c.menu.setEnabled(itemForceDischarge, false)
			}
			c.updateChargeOnceControls()
		}
	}
}

func (c *menuController) refreshDisableSchedules() {
	rawConfig, err := c.api.GetConfig()
	if err != nil {
		logrus.WithError(err).Debug("Failed to refresh temporary disable schedule")
		return
	}
	conf := config.NewFileFromConfig(rawConfig, "")
	c.updateDisableSchedules(conf)

	currentCharge, err := c.api.GetCurrentCharge()
	if err != nil {
		logrus.WithError(err).Debug("Failed to refresh current charge")
		currentCharge = c.currentCharge
	}
	c.updateChargeOnce(conf, currentCharge)

	if c.capabilities.AdapterControl {
		if adapter, err := c.api.GetAdapter(); err == nil {
			c.updateAdapterState(adapter)
		}
	}
}

func (c *menuController) updateDisableSchedules(conf config.Config) {
	c.updateDisableSchedule(conf)
	c.updateAdapterDisableSchedule(conf)
}

func (c *menuController) updateDisableSchedule(conf config.Config) {
	until := conf.DisableUntil()
	scheduled := !until.IsZero()
	c.disableScheduled = scheduled
	for _, item := range disableLimitActionItems {
		c.menu.setEnabled(item, !scheduled)
	}
	c.menu.setHidden(itemDisableLimitCountdown, !scheduled)

	if !scheduled {
		c.menu.setTooltip(itemDisableLimit, disableLimitTooltip)
		c.updateChargeOnceControls()
		return
	}

	c.menu.setEnabled(itemCalibrationStart, false)
	c.menu.setEnabled(itemDisableLimit, true)
	c.menu.setTitle(
		itemDisableLimitCountdown,
		temporaryDisableCountdownTitle(conf.PreDisableLimit(), time.Until(until)),
	)
	c.menu.setTooltip(itemDisableLimit, disableLimitScheduledTooltip)
	c.menu.setTooltip(itemDisableLimitCountdown, disableLimitScheduledTooltip)
	c.updateChargeOnceControls()
}

func (c *menuController) updateAdapterDisableSchedule(conf config.Config) {
	until := conf.AdapterDisableUntil()
	c.adapterDisableScheduled = !until.IsZero()
	c.menu.setHidden(itemForceDischargeCountdown, !c.adapterDisableScheduled)

	if !c.adapterDisableScheduled {
		c.menu.setTooltip(itemForceDischarge, forceDischargeTooltip)
		c.updateForceDischargeControls()
		c.updateChargeOnceControls()
		return
	}

	c.menu.setEnabled(itemCalibrationStart, false)
	c.menu.setTitle(itemForceDischargeCountdown, temporaryAdapterDisableCountdownTitle(time.Until(until)))
	c.menu.setTooltip(itemForceDischarge, forceDischargeScheduledTooltip)
	c.menu.setTooltip(itemForceDischargeCountdown, forceDischargeScheduledTooltip)
	c.updateForceDischargeControls()
	c.updateChargeOnceControls()
}

// updateChargeOnce refreshes the one-time charge items from the latest config
// and charge reading.
func (c *menuController) updateChargeOnce(conf config.Config, currentCharge int) {
	c.upperLimit = conf.UpperLimit()
	c.currentCharge = currentCharge
	c.chargeOnceTarget = conf.ChargeOnceTarget()
	c.updateChargeOnceControls()
	c.updateForceDischargeControls()
}

func (c *menuController) updateChargeOnceControls() {
	active := c.chargeOnceTarget > 0

	c.menu.setTitle(itemChargeOnceLimit, chargeOnceLimitTitle(c.upperLimit))
	c.menu.setHidden(itemChargeOnceStatus, !active)
	c.menu.setHidden(itemChargeOnceCancel, !active)
	if active {
		c.menu.setTitle(itemChargeOnceStatus, chargeOnceStatusTitle(c.chargeOnceTarget, c.currentCharge))
	}

	c.menu.setEnabled(itemChargeOnceLimit, canChargeOnceToLimit(
		c.calibrationPhase, c.disableScheduled, c.adapterDisableScheduled, c.chargeOnceTarget,
		c.upperLimit, c.currentCharge, c.capabilities.ChargeControlMode,
		c.capabilities.AdapterControl, c.adapterKnown, c.adapterEnabled))
	c.menu.setEnabled(itemChargeOnceFull, canChargeOnceToFull(
		c.calibrationPhase, c.disableScheduled, c.adapterDisableScheduled, c.chargeOnceTarget,
		c.upperLimit, c.currentCharge, c.capabilities.AdapterControl, c.adapterKnown, c.adapterEnabled))
	c.menu.setEnabled(itemChargeOnceCancel, active)
}

func (c *menuController) updateAdapterState(enabled bool) {
	c.adapterEnabled = enabled
	c.adapterKnown = true
	c.updateForceDischargeControls()
	c.updateChargeOnceControls()
}

func (c *menuController) updateForceDischargeControls() {
	canControl := c.adapterKnown && c.calibrationPhase == calibration.PhaseIdle
	canStart := c.adapterKnown && c.adapterEnabled &&
		canStartForceDischarge(c.calibrationPhase, c.adapterDisableScheduled, c.chargeOnceTarget)
	for _, item := range forceDischargeActionItems {
		c.menu.setEnabled(item, canStart)
	}
	c.menu.setEnabled(itemForceDischargeStop, canControl && (!c.adapterEnabled || c.adapterDisableScheduled))
	c.menu.setEnabled(itemForceDischarge, c.adapterKnown && canOpenForceDischargeMenu(c.calibrationPhase, c.adapterDisableScheduled))
}

func (c *menuController) setStateError(message string, err error) {
	logrus.WithError(err).Error(message)
	c.menu.setTitle(itemState, "State: Error")
}

func (c *menuController) setCompatibility(installed bool, capabilities compatibility.Capabilities, needsUpgrade bool) {
	if value := os.Getenv("BATT_GUI_NO_COMPATIBILITY_CHECK"); value == "1" || value == "true" {
		return
	}
	c.capabilities = capabilities
	c.menu.setStatusIcon(installed, capabilities.ChargingControl, needsUpgrade)

	usable := installed && capabilities.ChargingControl && !needsUpgrade
	c.menu.setHidden(itemPowerFlow, !usable)
	c.menu.setHidden(itemInstall, installed)
	c.menu.setHidden(itemUpgrade, !installed || (!needsUpgrade && capabilities.ChargingControl))
	c.menu.setHidden(itemState, !installed || !capabilities.ChargingControl)
	c.menu.setHidden(itemCurrentLimit, !installed || !capabilities.ChargingControl)
	c.menu.setHidden(itemQuickLimits, !usable)
	for _, item := range quickLimitItems {
		// macOS only offers a fixed set of limits on some firmware.
		c.menu.setHidden(item, !usable || !capabilities.SupportsLimit(quickLimitForItem(item)))
	}
	for _, item := range chargeOnceItems {
		c.menu.setHidden(item, !usable)
	}
	if usable {
		// The status and cancel items only belong on screen while a one-time
		// charge is running.
		c.updateChargeOnceControls()
	}

	c.menu.setHidden(itemAdvanced, !installed)
	c.menu.setHidden(itemMagSafe, !usable || !capabilities.MagSafeLED)
	c.menu.setHidden(itemPreventIdleSleep, !usable || !capabilities.SleepHooks)
	c.menu.setHidden(itemDisableChargingPreSleep, !usable || !capabilities.SleepHooks)
	c.menu.setHidden(itemPreventSystemSleep, !usable || !capabilities.SleepHooks)
	c.menu.setHidden(itemForceDischarge, !usable || !capabilities.AdapterControl)
	c.menu.setHidden(itemAutoCalibration, !usable || !capabilities.Calibration)
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
	c.calibrationPhase = status.Phase
	isIdle := status.Phase == calibration.PhaseIdle
	switch {
	case isIdle:
		c.menu.setTitle(itemAutoCalibration, "Auto Calibration (Experimental)...")
	case status.Paused:
		c.menu.setTitle(itemAutoCalibration, "Auto Calibration (Experimental) Paused...")
	default:
		c.menu.setTitle(itemAutoCalibration, "Auto Calibration (Experimental) In Progress...")
	}

	c.menu.setEnabled(itemCalibrationStart, canStartCalibration(status.Phase, c.disableScheduled, c.adapterDisableScheduled))
	c.menu.setEnabled(itemCalibrationCancel, !isIdle)
	c.menu.setEnabled(itemCalibrationPause, !isIdle && !status.Paused)
	c.menu.setEnabled(itemCalibrationResume, status.Paused)
	if title, ok := c.calibrationStatusTitle(status); ok {
		c.menu.setTitle(itemCalibrationStatus, title)
	}

	settingsEnabled := isIdle || status.Phase == calibration.PhaseError || status.Paused
	for _, item := range []menuItem{
		itemUninstall,
	} {
		c.menu.setEnabled(item, settingsEnabled)
	}
	for _, item := range quickLimitItems {
		c.menu.setEnabled(item, canSetChargeLimit(status.Phase))
	}
	// A persisted conflict may contain both states after a restart. Keep the
	// submenu openable only to show its countdown; its actions remain disabled.
	c.menu.setEnabled(itemDisableLimit, canOpenDisableLimitMenu(status.Phase, c.disableScheduled))
	c.updateForceDischargeControls()
	c.updateChargeOnceControls()
}

func canStartCalibration(phase calibration.Phase, disableScheduled, adapterDisableScheduled bool) bool {
	return phase == calibration.PhaseIdle && !disableScheduled && !adapterDisableScheduled
}

func canOpenDisableLimitMenu(phase calibration.Phase, disableScheduled bool) bool {
	return phase == calibration.PhaseIdle || disableScheduled
}

func canSetChargeLimit(phase calibration.Phase) bool {
	return phase == calibration.PhaseIdle
}

// canStartChargeOnce reports whether a new one-time charge may be started.
// Calibration and a temporary disable both drive the charge limit themselves,
// and a temporarily disabled power adapter cuts the power the charge needs.
func canStartChargeOnce(
	phase calibration.Phase,
	disableScheduled, adapterDisableScheduled bool,
	chargeOnceTarget int,
	adapterControl, adapterKnown, adapterEnabled bool,
) bool {
	adapterAvailable := !adapterControl || (adapterKnown && adapterEnabled)
	return phase == calibration.PhaseIdle && !disableScheduled && !adapterDisableScheduled &&
		chargeOnceTarget == 0 && adapterAvailable
}

// canChargeOnceToLimit reports whether charging to the configured limit right
// now would do anything. Firmware considers target-1 the top of its narrowest
// legal one-time band, matching daemon admission and completion.
func canChargeOnceToLimit(
	phase calibration.Phase,
	disableScheduled, adapterDisableScheduled bool,
	chargeOnceTarget, upperLimit, currentCharge int,
	chargeControlMode compatibility.ChargeControlMode,
	adapterControl, adapterKnown, adapterEnabled bool,
) bool {
	targetReached := currentCharge >= upperLimit
	if upperLimit < 100 && chargeControlMode == compatibility.ChargeControlFirmware {
		targetReached = currentCharge >= upperLimit-1
	}
	return chargeControlMode != compatibility.ChargeControlNative &&
		canStartChargeOnce(phase, disableScheduled, adapterDisableScheduled, chargeOnceTarget,
			adapterControl, adapterKnown, adapterEnabled) && chargeLimitActive(upperLimit) && !targetReached
}

// canChargeOnceToFull reports whether a one-time charge to 100% would do
// anything.
func canChargeOnceToFull(
	phase calibration.Phase,
	disableScheduled, adapterDisableScheduled bool,
	chargeOnceTarget, upperLimit, currentCharge int,
	adapterControl, adapterKnown, adapterEnabled bool,
) bool {
	return canStartChargeOnce(phase, disableScheduled, adapterDisableScheduled, chargeOnceTarget,
		adapterControl, adapterKnown, adapterEnabled) && chargeLimitActive(upperLimit) && currentCharge < 100
}

// chargeLimitActive reports whether the daemon reported a limit that batt
// actually enforces. Zero means the menu has not heard from the daemon yet.
func chargeLimitActive(upperLimit int) bool {
	return upperLimit >= 10 && upperLimit < 100
}

func canOpenForceDischargeMenu(phase calibration.Phase, adapterDisableScheduled bool) bool {
	return phase == calibration.PhaseIdle || adapterDisableScheduled
}

// canStartForceDischarge reports whether force discharge may be started. The
// daemon rejects cutting power while a one-time charge runs, because the charge
// would never finish.
func canStartForceDischarge(phase calibration.Phase, adapterDisableScheduled bool, chargeOnceTarget int) bool {
	return phase == calibration.PhaseIdle && !adapterDisableScheduled && chargeOnceTarget == 0
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
