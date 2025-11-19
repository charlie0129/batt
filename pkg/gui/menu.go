package gui

import (
	"context"
	"fmt"
	"math"
	"os"

	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
	"github.com/progrium/darwinkit/objc"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/client"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
	"github.com/charlie0129/batt/pkg/version"
)

// menuController owns the menu updates and avoids darwinkit delegate closures.
type menuController struct {
	api         *client.Client
	menubarIcon appkit.StatusItem

	// Power Flow
	powerFlowSubMenuItem appkit.MenuItem
	systemItem           appkit.MenuItem
	adapterItem          appkit.MenuItem
	batteryItem          appkit.MenuItem

	// Core items
	installItem      appkit.MenuItem
	upgradeItem      appkit.MenuItem
	stateItem        appkit.MenuItem
	currentLimitItem appkit.MenuItem
	quickLimitsItem  appkit.MenuItem
	quickLimitsItems map[int]appkit.MenuItem

	// Advanced
	advancedSubMenuItem   appkit.MenuItem
	controlMagSafeLEDItem appkit.MenuItem

	controlMagSafeEnableItem    appkit.MenuItem
	controlMagSafeDisableItem   appkit.MenuItem
	controlMagSafeAlwaysOffItem appkit.MenuItem

	preventIdleSleepItem        appkit.MenuItem
	disableChargingPreSleepItem appkit.MenuItem
	preventSystemSleepItem      appkit.MenuItem
	forceDischargeItem          appkit.MenuItem
	uninstallItem               appkit.MenuItem

	// Auto Calibration
	autoCalSubMenuItem appkit.MenuItem
	calStatusItem      appkit.MenuItem
	calStartItem       appkit.MenuItem
	calPauseItem       appkit.MenuItem
	calResumeItem      appkit.MenuItem
	calCancelItem      appkit.MenuItem

	// Quit/disable
	disableItem appkit.MenuItem

	// Calibration cached parameters
	calThreshold   int
	calHoldMinutes int

	// eventCancel cancels the SSE event subscription goroutine
	eventCancel context.CancelFunc
}

func (c *menuController) onWillOpen() {
	c.refreshOnOpen()
	c.updateTelemetryOnce()
}

func (c *menuController) onDidClose() {}

func (c *menuController) onTimerTick() {
	c.updateTelemetryOnce()
}

func (c *menuController) toggleMenusRequiringInstall(battInstalled, capable, needUpgrade bool) {
	if v := os.Getenv("BATT_GUI_NO_COMPATIBILITY_CHECK"); v == "1" || v == "true" {
		return
	}
	setMenubarImage(c.menubarIcon, battInstalled, capable, needUpgrade)

	// Visible when installed, capable, and no upgrade needed.
	c.powerFlowSubMenuItem.SetHidden(!battInstalled || !capable || needUpgrade)

	c.installItem.SetHidden(battInstalled)
	// Show when installed AND (needs upgrade OR not capable)
	c.upgradeItem.SetHidden(!battInstalled || (!needUpgrade && capable))
	// Show when installed AND capable
	c.stateItem.SetHidden(!battInstalled || !capable)
	c.currentLimitItem.SetHidden(!battInstalled || !capable)

	// Show when installed AND capable AND no upgrade needed
	c.quickLimitsItem.SetHidden(!battInstalled || !capable || needUpgrade)
	for _, it := range c.quickLimitsItems {
		it.SetHidden(!battInstalled || !capable || needUpgrade)
	}

	c.advancedSubMenuItem.SetHidden(!battInstalled)
	c.controlMagSafeLEDItem.SetHidden(!battInstalled || !capable || needUpgrade)
	c.preventIdleSleepItem.SetHidden(!battInstalled || !capable || needUpgrade)
	c.disableChargingPreSleepItem.SetHidden(!battInstalled || !capable || needUpgrade)
	c.preventSystemSleepItem.SetHidden(!battInstalled || !capable || needUpgrade)
	c.forceDischargeItem.SetHidden(!battInstalled || !capable || needUpgrade)
	c.autoCalSubMenuItem.SetHidden(!battInstalled || !capable || needUpgrade)
	c.uninstallItem.SetHidden(!battInstalled)

	c.disableItem.SetHidden(!battInstalled || !capable || needUpgrade)
}

func (c *menuController) refreshOnOpen() {
	rawConfig, err := c.api.GetConfig()
	if err != nil {
		logrus.WithError(err).Error("Failed to get config")
		c.toggleMenusRequiringInstall(false, false, false)
		return
	}
	capable, err := c.api.GetChargingControlCapable()
	if err != nil {
		logrus.WithError(err).Error("Failed to get charging capablility")
		c.toggleMenusRequiringInstall(true, false, false)
		return
	}
	daemonVersion, err := c.api.GetVersion()
	if err != nil {
		logrus.WithError(err).Error("Failed to get version")
		c.toggleMenusRequiringInstall(true, capable, true)
	} else {
		c.toggleMenusRequiringInstall(true, capable, daemonVersion != version.Version)
	}
	logrus.WithField("daemonVersion", daemonVersion).WithField("clientVersion", version.Version).Info("Got daemon")

	isCharging, err := c.api.GetCharging()
	if err != nil {
		logrus.WithError(err).Error("Failed to get charging state")
		c.stateItem.SetTitle("State: Error")
		return
	}
	isPluggedIn, err := c.api.GetPluggedIn()
	if err != nil {
		logrus.WithError(err).Error("Failed to get plugged in state")
		c.stateItem.SetTitle("State: Error")
		return
	}
	currentCharge, err := c.api.GetCurrentCharge()
	if err != nil {
		logrus.WithError(err).Error("Failed to get current charge")
		c.stateItem.SetTitle("State: Error")
		return
	}
	batteryInfo, err := c.api.GetBatteryInfo()
	if err != nil {
		logrus.WithError(err).Error("Failed to get battery info")
		c.stateItem.SetTitle("State: Error")
		return
	}

	conf := config.NewFileFromConfig(rawConfig, "")
	logrus.WithFields(conf.LogrusFields()).Info("Got config")
	// Cache calibration params for formatting
	c.calThreshold = conf.CalibrationDischargeThreshold()
	c.calHoldMinutes = conf.CalibrationHoldDurationMinutes()
	c.currentLimitItem.SetTitle(fmt.Sprintf("Current Limit: %d%%", conf.UpperLimit()))
	for limit, item := range c.quickLimitsItems {
		setCheckboxItem(item, limit == conf.UpperLimit())
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
	c.stateItem.SetTitle("State: " + state)
	if !isCharging && isPluggedIn && conf.UpperLimit() < 100 && currentCharge < conf.LowerLimit() {
		c.stateItem.SetTitle("State: Will Charge Soon")
	}

	magSafeMode := conf.ControlMagSafeLED()
	switch magSafeMode {
	case config.ControlMagSafeModeEnabled:
		setCheckboxItem(c.controlMagSafeEnableItem, true)
		setCheckboxItem(c.controlMagSafeDisableItem, false)
		setCheckboxItem(c.controlMagSafeAlwaysOffItem, false)
	case config.ControlMagSafeModeAlwaysOff:
		setCheckboxItem(c.controlMagSafeEnableItem, false)
		setCheckboxItem(c.controlMagSafeDisableItem, false)
		setCheckboxItem(c.controlMagSafeAlwaysOffItem, true)
	default:
		setCheckboxItem(c.controlMagSafeEnableItem, false)
		setCheckboxItem(c.controlMagSafeDisableItem, true)
		setCheckboxItem(c.controlMagSafeAlwaysOffItem, false)
	}

	setCheckboxItem(c.preventIdleSleepItem, conf.PreventIdleSleep())
	setCheckboxItem(c.disableChargingPreSleepItem, conf.DisableChargingPreSleep())
	setCheckboxItem(c.preventSystemSleepItem, conf.PreventSystemSleep())
	if adapter, err := c.api.GetAdapter(); err == nil {
		setCheckboxItem(c.forceDischargeItem, !adapter)
	} else {
		logrus.WithError(err).Error("Failed to get adapter")
		c.forceDischargeItem.SetEnabled(false)
	}
}

// updateTelemetryOnce fetches both power and calibration in a single call and updates the UI.
func (c *menuController) updateTelemetryOnce() {
	tr, err := c.api.GetTelemetry(true, true)
	if err != nil || tr == nil {
		if err != nil {
			logrus.WithError(err).Debug("GetTelemetry failed")
		}
		return
	}
	// Power section
	if tr.Power != nil {
		info := tr.Power
		c.systemItem.SetAttributedTitle(formatPowerString("System", info.Calculations.SystemPower))
		c.adapterItem.SetAttributedTitle(formatPowerString("Adapter", info.Calculations.ACPower))
		c.batteryItem.SetAttributedTitle(formatPowerString("Battery", info.Calculations.BatteryPower))
	}
	// Calibration section
	if tr.Calibration != nil {
		st := tr.Calibration
		isIdle := st.Phase == calibration.PhaseIdle
		// Title of submenu
		if !isIdle {
			if st.Paused {
				c.autoCalSubMenuItem.SetTitle("Auto Calibration (Paused)")
			} else {
				c.autoCalSubMenuItem.SetTitle("Auto Calibration (In Progress)")
			}
		} else {
			c.autoCalSubMenuItem.SetTitle("Auto Calibration…")
		}
		// Enable/disable action items
		c.calStartItem.SetEnabled(isIdle)
		c.calCancelItem.SetEnabled(!isIdle)
		if st.Paused {
			c.calPauseItem.SetEnabled(false)
			c.calResumeItem.SetEnabled(true)
		} else {
			c.calPauseItem.SetEnabled(!isIdle)
			c.calResumeItem.SetEnabled(false)
		}

		// Format status line
		switch st.Phase {
		case calibration.PhaseIdle:
			c.calStatusItem.SetTitle("Status: Idle")
		case calibration.PhaseDischarge:
			c.calStatusItem.SetTitle(fmt.Sprintf("Status: Discharging %d%% → %d%%", st.ChargePercent, c.calThreshold))
		case calibration.PhaseCharge:
			c.calStatusItem.SetTitle(fmt.Sprintf("Status: Charging %d%% → 100%%", st.ChargePercent))
		case calibration.PhaseHold:
			hrs := st.RemainingHoldSecs / 3600
			mins := (st.RemainingHoldSecs % 3600) / 60
			secs := st.RemainingHoldSecs % 60
			c.calStatusItem.SetTitle(fmt.Sprintf("Status: Holding %02d:%02d:%02d left", hrs, mins, secs))
		case calibration.PhasePostHold:
			if st.TargetPercent > 0 {
				c.calStatusItem.SetTitle(fmt.Sprintf("Status: Discharging %d%% → %d%%", st.ChargePercent, st.TargetPercent))
			} else {
				c.calStatusItem.SetTitle("Status: Discharging to previous limit…")
			}
		case calibration.PhaseRestore:
			c.calStatusItem.SetTitle("Status: Restoring settings…")
		case calibration.PhaseError:
			if st.Message != "" {
				c.calStatusItem.SetTitle("Status: Error - " + st.Message)
			} else {
				c.calStatusItem.SetTitle("Status: Error")
			}
		}

		// Do not let the user change settings when we are trying to calibrate.
		if st.Phase == calibration.PhaseIdle || st.Phase == calibration.PhaseError {
			c.forceDischargeItem.SetEnabled(true)
			c.uninstallItem.SetEnabled(true)
			c.disableItem.SetEnabled(true)
			for _, i := range c.quickLimitsItems {
				i.SetEnabled(true)
			}
		} else {
			c.forceDischargeItem.SetEnabled(false)
			c.uninstallItem.SetEnabled(false)
			c.disableItem.SetEnabled(false)
			for _, i := range c.quickLimitsItems {
				i.SetEnabled(false)
			}
		}
	}
}

func formatPowerString(label string, value float64) foundation.AttributedString {
	var color appkit.Color
	sign := " " // Default to a space for alignment. This is crucial.

	if label == "System" {
		color = appkit.Color_LabelColor()
	} else {
		switch {
		case value > 0:
			color = appkit.Color_SystemGreenColor()
			sign = "+"
		case value < 0:
			color = appkit.Color_SystemRedColor()
			sign = "-"
		default: // value is 0
			color = appkit.Color_LabelColor()
		}
	}

	// Use a monospaced font for alignment.
	font := appkit.Font_MonospacedSystemFontOfSizeWeight(12, appkit.FontWeightRegular)

	// Format the string with padding for alignment.
	// %-8s  : The label, left-aligned and padded to 8 characters.
	// %s    : Our sign character (+, -, or space).
	// %7.2f : The numeric value, formatted to be 7 characters wide with 2 decimal places.
	//         This pads smaller numbers (like 5.25) with a space to align with larger ones (like 15.25).
	//         Using math.Abs() is critical to prevent a double negative sign.
	fullString := fmt.Sprintf("%-8s %s%7.2fW", label+":", sign, math.Abs(value))

	attrStr := foundation.NewMutableAttributedStringWithString(fullString)

	// Define the range for the label (e.g., "System: ")
	// The location where the value starts is now fixed because of our padding.
	// Padded label (8) + space (1) = 9.
	valueLocation := 9
	labelRange := foundation.Range{
		Location: 0,
		Length:   uint64(valueLocation),
	}
	// Define the range for the value (e.g., "+  5.25W")
	valueRange := foundation.Range{
		Location: uint64(valueLocation),
		Length:   uint64(len(fullString) - valueLocation),
	}

	// Set the label part to the standard secondary gray color.
	attrStr.AddAttributeValueRange(foundation.AttributedStringKey("NSColor"), appkit.Color_SecondaryLabelColor(), labelRange)
	// Set the value part to its specific color (green, red, or white).
	attrStr.AddAttributeValueRange(foundation.AttributedStringKey("NSColor"), color, valueRange)

	// Apply the monospaced font to the entire string.
	attrStr.AddAttributeValueRange(foundation.AttributedStringKey("NSFont"), font, foundation.Range{Location: 0,
		Length: uint64(len(fullString))})
	return attrStr.AttributedString
}

func setMenubarImage(menubarStatusItem appkit.StatusItem, daemonInstalled, capable, needUpgrade bool) {
	if !daemonInstalled {
		menubarStatusItem.Button().SetImage(appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("batteryblock.slash", "batt daemon not installed"))
		return
	}
	if !capable {
		menubarStatusItem.Button().SetImage(appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("minus.plus.batteryblock.exclamationmark", "Your machine cannot run batt"))
		return
	}
	if needUpgrade {
		menubarStatusItem.Button().SetImage(appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("fluid.batteryblock", "batt needs upgrade"))
		return
	}
	menubarStatusItem.Button().SetImage(appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("minus.plus.batteryblock", "batt icon"))
}

func checkBoxItem(title, charCode string, cb func(checked bool)) appkit.MenuItem {
	return appkit.NewMenuItemWithAction(title, charCode, func(sender objc.Object) {
		// Cast sender to MenuItem to manipulate its state
		menuItem := appkit.MenuItemFrom(sender.Ptr())

		// Toggle state
		newState := menuItem.State() == appkit.ControlStateValueOff

		// Update state in UI
		setCheckboxItem(menuItem, newState)

		cb(newState)
	})
}

func setCheckboxItem(menuItem appkit.MenuItem, checked bool) {
	if checked {
		menuItem.SetState(appkit.ControlStateValueOn)
	} else {
		menuItem.SetState(appkit.ControlStateValueOff)
	}
}
