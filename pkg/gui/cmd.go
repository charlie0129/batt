package gui

import (
	"fmt"
	"math"
	"os"

	pkgerrors "github.com/pkg/errors"
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
	"github.com/progrium/darwinkit/objc"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/client"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
	"github.com/charlie0129/batt/pkg/version"

	cgo "runtime/cgo"
)

func NewGUICommand(unixSocketPath string, groupID string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "gui",
		Short:   "Start the batt GUI (debug)",
		GroupID: groupID,
		Long: `Start the batt GUI.

This command should not be called directly by the user. Users should use the .app bundle to start the GUI.`,
		Run: func(_ *cobra.Command, _ []string) {
			Run(unixSocketPath)
		},
	}

	return cmd
}

func Run(unixSocketPath string) {
	apiClient := client.NewClient(unixSocketPath)

	app := appkit.Application_SharedApplication()
	// Set up the menubar immediately to avoid using a dynamic
	// Objective-C closure for NSApplicationDidFinishLaunching.
	logrus.WithField("version", version.Version).WithField("gitCommit", version.GitCommit).Info("batt gui")
	cleanup := addMenubar(app, apiClient)
	defer cleanup()

	app.Run()
}

//nolint:gocyclo
func addMenubar(app appkit.Application, apiClient *client.Client) func() {
	menubarIcon := appkit.StatusBar_SystemStatusBar().StatusItemWithLength(appkit.VariableStatusItemLength)
	objc.Retain(&menubarIcon)
	setMenubarImage(menubarIcon, false, false, false)
	menu := appkit.NewMenuWithTitle("batt")
	menu.SetAutoenablesItems(false)

	// ==================== POWER FLOW (selector-based, ObjC-observed) ====================
	powerFlowMenu := appkit.NewMenuWithTitle("Power Flow")
	powerFlowMenu.SetAutoenablesItems(false)
	powerFlowSubMenuItem := appkit.NewSubMenuItem(powerFlowMenu)
	powerFlowSubMenuItem.SetTitle("Power Flow")

	// Items with attributed titles
	powerSystemItem := appkit.NewMenuItemWithAction("", "", func(sender objc.Object) {})
	powerSystemItem.SetEnabled(true) // Changed to true for readability
	powerSystemItem.SetAttributedTitle(formatPowerString("System", 0))
	powerFlowMenu.AddItem(powerSystemItem)

	powerAdapterItem := appkit.NewMenuItemWithAction("", "", func(sender objc.Object) {})
	powerAdapterItem.SetEnabled(true) // Changed to true for readability
	powerAdapterItem.SetAttributedTitle(formatPowerString("Adapter", 0))
	powerFlowMenu.AddItem(powerAdapterItem)

	powerBatteryItem := appkit.NewMenuItemWithAction("", "", func(sender objc.Object) {})
	powerBatteryItem.SetEnabled(true) // Changed to true for readability
	powerBatteryItem.SetAttributedTitle(formatPowerString("Battery", 0))
	powerFlowMenu.AddItem(powerBatteryItem)

	// Add Power Flow submenu near the top
	menu.AddItem(powerFlowSubMenuItem)

	// ==================== INSTALL & STATES ====================

	uninstallOrUpgrade := func(sender objc.Object) {
		exe, err := os.Executable()
		if err != nil {
			logrus.WithError(err).Error("Failed to get executable path")
			showAlert("Failed to get executable path", err.Error())
			return
		}

		err = installDaemon(exe)
		if err != nil {
			logrus.WithError(err).Error("Failed to install daemon")
			showAlert("Installation failed", err.Error())
			return
		}

		err = startAppAtBoot()
		if err != nil {
			logrus.WithError(err).Error("Failed to start app at boot")
			showAlert("Failed to start app at boot", err.Error())
			return
		}

		setMenubarImage(menubarIcon, true, true, false)
	}

	upgradeItem := appkit.NewMenuItemWithAction("Upgrade Daemon...", "u", uninstallOrUpgrade)
	upgradeItem.SetToolTip(`Your batt daemon is not compatible with this client version and needs to be upgraded. This is usually caused by a new client version that requires a new daemon version. You can upgrade the batt daemon by running this command.`)
	menu.AddItem(upgradeItem)

	installItem := appkit.NewMenuItemWithAction("Install Daemon...", "i", uninstallOrUpgrade)
	installItem.SetToolTip(`Install the batt daemon. batt daemon is a component that controls charging. You must enter your password to install it because controlling charging is a privileged action.`)
	menu.AddItem(installItem)

	stateItem := appkit.NewMenuItemWithAction("Loading...", "", func(sender objc.Object) {})
	stateItem.SetEnabled(false)
	menu.AddItem(stateItem)

	currentLimitItem := appkit.NewMenuItemWithAction("Loading...", "", func(sender objc.Object) {})
	currentLimitItem.SetEnabled(false)
	menu.AddItem(currentLimitItem)

	// ==================== QUICK LIMITS ====================
	menu.AddItem(appkit.MenuItem_SeparatorItem())

	quickLimitsItem := appkit.NewMenuItemWithAction("Quick Limits", "", func(sender objc.Object) {})
	quickLimitsItem.SetEnabled(false)
	menu.AddItem(quickLimitsItem)

	setQuickLimitsItems := map[int]appkit.MenuItem{}

	for _, i := range []int{50, 60, 70, 80, 90} {
		setQuickLimitsItems[i] = appkit.NewMenuItemWithAction(fmt.Sprintf("Set %d%% Limit", i), fmt.Sprintf("%d", i), func(sender objc.Object) {
			ret, err := apiClient.SetLimit(i)
			if err != nil {
				logrus.WithError(err).Error("Failed to set limit")
				showAlert("Failed to set limit", ret+err.Error())
				return
			}
		})
		menu.AddItem(setQuickLimitsItems[i])
	}

	// ==================== ADVANCED ====================

	menu.AddItem(appkit.MenuItem_SeparatorItem())

	advancedMenu := appkit.NewMenuWithTitle("Advanced")
	advancedMenu.SetAutoenablesItems(false)
	advancedSubMenuItem := appkit.NewSubMenuItem(advancedMenu)
	advancedSubMenuItem.SetTitle("Advanced")
	menu.AddItem(advancedSubMenuItem)

	controlMagSafeLEDMenu := appkit.NewMenuWithTitle("Control MagSafe LED")
	controlMagSafeLEDItem := appkit.NewSubMenuItem(controlMagSafeLEDMenu)
	controlMagSafeLEDItem.SetTitle("Control MagSafe LED")
	controlMagSafeLEDItem.SetToolTip(`Let batt control MagSafe LED to reflect the charging state of your MacBook (or force it off).

Note that you must have a MagSafe LED on your MacBook to use this feature.`)
	advancedMenu.AddItem(controlMagSafeLEDItem)

	var (
		controlMagSafeEnableItem    appkit.MenuItem
		controlMagSafeDisableItem   appkit.MenuItem
		controlMagSafeAlwaysOffItem appkit.MenuItem
	)

	controlMagSafeEnableItem = appkit.NewMenuItemWithAction("Enable", "", func(sender objc.Object) {
		setCheckboxItem(controlMagSafeEnableItem, true)
		setCheckboxItem(controlMagSafeDisableItem, false)
		setCheckboxItem(controlMagSafeAlwaysOffItem, false)

		_, err := apiClient.SetControlMagSafeLED(config.ControlMagSafeModeEnabled)
		if err != nil {
			logrus.WithError(err).Error("Failed to set control mag safe LED")
			showAlert("Failed to set MagSafe LED control", err.Error())
			return
		}
	})
	controlMagSafeLEDMenu.AddItem(controlMagSafeEnableItem)

	controlMagSafeDisableItem = appkit.NewMenuItemWithAction("Disable", "", func(sender objc.Object) {
		setCheckboxItem(controlMagSafeEnableItem, false)
		setCheckboxItem(controlMagSafeDisableItem, true)
		setCheckboxItem(controlMagSafeAlwaysOffItem, false)

		_, err := apiClient.SetControlMagSafeLED(config.ControlMagSafeModeDisabled)
		if err != nil {
			logrus.WithError(err).Error("Failed to set control mag safe LED")
			showAlert("Failed to set MagSafe LED control", err.Error())
			return
		}
	})
	controlMagSafeLEDMenu.AddItem(controlMagSafeDisableItem)

	controlMagSafeAlwaysOffItem = appkit.NewMenuItemWithAction("Always-off", "", func(sender objc.Object) {
		setCheckboxItem(controlMagSafeEnableItem, false)
		setCheckboxItem(controlMagSafeDisableItem, false)
		setCheckboxItem(controlMagSafeAlwaysOffItem, true)

		_, err := apiClient.SetControlMagSafeLED(config.ControlMagSafeModeAlwaysOff)
		if err != nil {
			logrus.WithError(err).Error("Failed to set control mag safe LED")
			showAlert("Failed to set MagSafe LED control", err.Error())
			return
		}
	})
	controlMagSafeLEDMenu.AddItem(controlMagSafeAlwaysOffItem)

	controlMagSafeEnableItem.SetToolTip(`Enable MagSafe LED control. The LED will reflect charging status:

- Green: Charge limit is reached and charging is stopped.
- Orange: Charging is in progress.
- Off: Woke from sleep, charging is off and batt is awaiting control.`)
	controlMagSafeDisableItem.SetToolTip(`Disable MagSafe LED control. The LED will stay in its default state (mostly orange).`)
	controlMagSafeAlwaysOffItem.SetToolTip(`Force the MagSafe LED to stay off regardless of charging state.`)

	preventIdleSleepItem := checkBoxItem("Prevent Idle Sleep when Charging", "", func(checked bool) {
		// Perform action based on new state
		_, err := apiClient.SetPreventIdleSleep(checked)
		if err != nil {
			logrus.WithError(err).Error("Failed to set prevent idle sleep")
			showAlert("Failed to set prevent idle sleep", err.Error())
			return
		}
	})
	preventIdleSleepItem.SetToolTip(`Set whether to prevent idle sleep during a charging session.

Due to macOS limitations, batt will be paused when your computer goes to sleep. As a result, when you are in a charging session and your computer goes to sleep, there is no way for batt to stop charging (since batt is paused by macOS) and the battery will charge to 100%. This option, together with "Disable Charging before Sleep", will prevent this from happening.

This option tells macOS NOT to go to sleep when the computer is in a charging session, so batt can continue to work until charging is finished. Note that it will only prevent **idle** sleep, when 1) charging is active 2) battery charge limit is enabled. So your computer can go to sleep as soon as a charging session is completed.

However, this options does not prevent manual sleep (limitation of macOS). For example, if you manually put your computer to sleep (by choosing the Sleep option in the top-left Apple menu) or close the lid, batt will still be paused and the issue mentioned above will still happen. This is where "Disable Charging before Sleep" comes in.`)
	advancedMenu.AddItem(preventIdleSleepItem)

	disableChargingPreSleepItem := checkBoxItem("Disable Charging before Sleep", "", func(checked bool) {
		// Perform action based on new state
		_, err := apiClient.SetDisableChargingPreSleep(checked)
		if err != nil {
			logrus.WithError(err).Error("Failed to set disable charging pre sleep")
			showAlert("Failed to set prevent idle sleep", err.Error())
			return
		}
	})
	disableChargingPreSleepItem.SetToolTip(`Set whether to disable charging before sleep if charge limit is enabled.

As described in "Prevent Idle Sleep when Charging", batt will be paused by macOS when your computer goes to sleep, and there is no way for batt to continue controlling battery charging. This option will disable charging just before sleep, so your computer will not overcharge during sleep, even if the battery charge is below the limit.`)
	advancedMenu.AddItem(disableChargingPreSleepItem)

	preventSystemSleepItem := checkBoxItem("Prevent System Sleep when Charging (Experimental)", "", func(checked bool) {
		// Perform action based on new state
		_, err := apiClient.SetPreventSystemSleep(checked)
		if err != nil {
			showAlert("Failed to set prevent system sleep", err.Error())
			return
		}
	})
	preventSystemSleepItem.SetToolTip(`Set whether to prevent system sleep during a charging session (experimental).

This option tells macOS to create power assertion, which prevents sleep, when all conditions are met:

1) charging is active
2) battery charge limit is enabled
3) computer is connected to charger.
So your computer can go to sleep as soon as a charging session is completed / charger disconnected.

Does similar thing to prevent-idle-sleep, but works for manual sleep too.

Note: please disable disable-charging-pre-sleep and prevent-idle-sleep, while this feature is in use`)
	advancedMenu.AddItem(preventSystemSleepItem)

	forceDischargeItem := checkBoxItem("Force Discharge...", "", func(checked bool) {
		if checked {
			alert := appkit.NewAlert()
			alert.SetIcon(appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("note.text", "notes"))
			alert.SetAlertStyle(appkit.AlertStyleInformational)
			alert.SetMessageText("Precautions")
			alert.SetInformativeText(`1. The lid of your MacBook MUST be open, otherwise your Mac will go to sleep immediately.
2. Be sure to come back and disable "Force Discharge" when you are done, otherwise the battery of your Mac will drain completely.`)
			alert.RunModal()
		}

		_, err := apiClient.SetAdapter(!checked)
		if err != nil {
			showAlert("Failed to set force discharge", err.Error())
			return
		}
	})
	forceDischargeItem.SetToolTip(`Cut power from the wall. This has the same effect as unplugging the power adapter, even if the adapter is physically plugged in.

This is useful when you want to use your battery to lower the battery charge, but you don't want to unplug the power adapter.

NOTE: if you are using Clamshell mode (using a Mac laptop with an external monitor and the lid closed), *cutting power will cause your Mac to go to sleep*. This is a limitation of macOS. There are ways to prevent this, but it is not recommended for most users.`)
	advancedMenu.AddItem(forceDischargeItem)

	// Auto Calibration menu (after Force Discharge)
	autoCalibrationItem := appkit.NewMenuWithTitle("Auto Calibration")
	autoCalibrationItem.SetAutoenablesItems(false)
	autoCalibrationSub := appkit.NewSubMenuItem(autoCalibrationItem)
	autoCalibrationSub.SetTitle("Auto Calibration…")
	advancedMenu.AddItem(autoCalibrationSub)

	// Sub-items
	calStatusItem := appkit.NewMenuItemWithAction("Status: Idle", "", func(sender objc.Object) {})
	calStatusItem.SetEnabled(false)
	autoCalibrationItem.AddItem(calStatusItem)

	calStartItem := appkit.NewMenuItemWithAction("Start", "", func(sender objc.Object) {
		alert := appkit.NewAlert()
		alert.SetIcon(appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("battery.100", "calibration"))
		alert.SetAlertStyle(appkit.AlertStyleInformational)
		alert.SetMessageText("Start Auto Calibration?")
		alert.SetInformativeText("This will:\n• Discharge to threshold, without sleep prevention.\n• Charge to 100%.\n• Hold at full for configured minutes.\n• Restore your original settings.\nYou can pause or cancel anytime from the menu.")
		// Explicitly add two buttons and compare first-button response, consistent with update flow
		alert.AddButtonWithTitle("Start")
		alert.AddButtonWithTitle("Cancel")
		response := alert.RunModal()
		if response != appkit.AlertFirstButtonReturn {
			logrus.Info("User cancelled auto calibration start")
			return
		}
		if _, err := apiClient.StartCalibration(); err != nil {
			showAlert("Failed to start calibration", err.Error())
			return
		}
		calStatusItem.SetTitle("Status: In Progress")
	})
	autoCalibrationItem.AddItem(calStartItem)

	calPauseItem := appkit.NewMenuItemWithAction("Pause", "", func(sender objc.Object) {
		if _, err := apiClient.PauseCalibration(); err != nil {
			showAlert("Failed to pause calibration", err.Error())
			return
		}
	})
	autoCalibrationItem.AddItem(calPauseItem)

	calResumeItem := appkit.NewMenuItemWithAction("Resume", "", func(sender objc.Object) {
		if _, err := apiClient.ResumeCalibration(); err != nil {
			showAlert("Failed to resume calibration", err.Error())
			return
		}
	})
	autoCalibrationItem.AddItem(calResumeItem)

	calCancelItem := appkit.NewMenuItemWithAction("Cancel", "", func(sender objc.Object) {
		if _, err := apiClient.CancelCalibration(); err != nil {
			showAlert("Failed to cancel calibration", err.Error())
			return
		}
		calStatusItem.SetTitle("Status: Idle")
	})
	autoCalibrationItem.AddItem(calCancelItem)

	advancedMenu.AddItem(appkit.MenuItem_SeparatorItem())

	versionItem := appkit.NewMenuItemWithAction("Version: "+version.Version, "", func(sender objc.Object) {})
	versionItem.SetEnabled(false)
	advancedMenu.AddItem(versionItem)

	uninstallItem := appkit.NewMenuItemWithAction("Uninstall Daemon...", "", func(sender objc.Object) {
		exe, err := os.Executable()
		if err != nil {
			logrus.WithError(err).Error("Failed to get executable path")
			showAlert("Failed to get executable path", err.Error())
			return
		}

		err = uninstallDaemon(exe)
		if err != nil {
			logrus.WithError(err).Error("Failed to uninstall daemon")
			showAlert("Failed to uninstall daemon", err.Error())
			return
		}

		err = UnregisterLoginItem()
		if err != nil {
			logrus.WithError(err).Error("Failed to unregister login item")
			showAlert("Failed to unregister login item", err.Error())
			return
		}

		setMenubarImage(menubarIcon, false, true, false)
	})
	uninstallItem.SetToolTip(`Uninstall the batt daemon. This will remove the batt daemon from your system. You must enter your password to uninstall it.

After uninstalling the batt daemon, no charging control will be present on your system and your Mac will charge to 100% as normal. The menubar app will still be present, but all options will be disabled. You can remove the menubar app by moving it to the trash.`)
	advancedMenu.AddItem(uninstallItem)

	// ==================== QUIT ====================
	menu.AddItem(appkit.MenuItem_SeparatorItem())
	disableItem := appkit.NewMenuItemWithAction("Disable Charging Limit", "d", func(sender objc.Object) {
		ret, err := apiClient.SetLimit(100)
		if err != nil {
			if !pkgerrors.Is(err, client.ErrDaemonNotRunning) {
				showAlert("Failed to set limit", ret+err.Error())
				return
			}
		}
	})
	disableItem.SetToolTip(`Disable battery charge limit and let your Mac charge to 100%. This almost has the same effect as uninstalling batt, but keeps the batt daemon installed.`)
	menu.AddItem(disableItem)

	menubarIcon.SetMenu(menu)

	// ==================== CALLBACKS & OBSERVER ====================
	ctrl := &menuController{
		api:                         apiClient,
		menubarIcon:                 menubarIcon,
		powerFlowSubMenuItem:        powerFlowSubMenuItem,
		installItem:                 installItem,
		upgradeItem:                 upgradeItem,
		stateItem:                   stateItem,
		currentLimitItem:            currentLimitItem,
		quickLimitsItem:             quickLimitsItem,
		setQuickLimitsItems:         setQuickLimitsItems,
		advancedSubMenuItem:         advancedSubMenuItem,
		controlMagSafeLEDItem:       controlMagSafeLEDItem,
		controlMagSafeEnableItem:    controlMagSafeEnableItem,
		controlMagSafeDisableItem:   controlMagSafeDisableItem,
		controlMagSafeAlwaysOffItem: controlMagSafeAlwaysOffItem,
		preventIdleSleepItem:        preventIdleSleepItem,
		disableChargingPreSleepItem: disableChargingPreSleepItem,
		preventSystemSleepItem:      preventSystemSleepItem,
		forceDischargeItem:          forceDischargeItem,
		uninstallItem:               uninstallItem,
		disableItem:                 disableItem,
		// Auto Calibration
		autoCalSubMenuItem: autoCalibrationSub,
		calStatusItem:      calStatusItem,
		calStartItem:       calStartItem,
		calPauseItem:       calPauseItem,
		calResumeItem:      calResumeItem,
		calCancelItem:      calCancelItem,
		// Power Flow items
		systemItem:  powerSystemItem,
		adapterItem: powerAdapterItem,
		batteryItem: powerBatteryItem,
	}

	h := cgo.NewHandle(ctrl)
	observerPtr := AttachPowerFlowObserver(menu, h)

	cleanupFunc := func() {
		logrus.Info("Cleaning up resources")
		ReleasePowerFlowObserver(observerPtr)
		h.Delete()
	}

	// The quit action is now simplified to only terminate the app.
	quitItem := appkit.NewMenuItemWithAction("Quit Menubar App", "q", func(sender objc.Object) {
		logrus.Info("Quitting client")
		app.Terminate(nil)
	})

	quitItem.SetToolTip(`Quit the batt menubar app, but keep the batt daemon running.

Since the batt daemon is still running, batt can continue to control charging. This is useful if you don't want the menubar icon to show up, but still want to use batt. When the client is not running, you can change batt settings using the command line interface (batt). To prevent the menubar app from starting at login, you can remove it in System Settings -> General -> Login Items & Extensions -> remove batt.app from the list (do NOT remove the batt daemon).

If you want to stop batt completely (menubar app and the daemon), you can use the "Disable Charging Limit" command. To uninstall, you can use the "Uninstall Daemon" command in the Advanced menu.`)
	menu.AddItem(quitItem)

	// The observer above will trigger onWillOpen/onDidClose/timer without using libffi closures.

	// Update icon onstart up
	{
		logrus.Info("Getting config")
		rawConfig, err := apiClient.GetConfig()
		if err != nil {
			logrus.WithError(err).Warnf("Failed to get config")
			ctrl.toggleMenusRequiringInstall(false, false, false)
			return cleanupFunc
		}
		conf := config.NewFileFromConfig(rawConfig, "")
		logrus.WithFields(conf.LogrusFields()).Info("Got config")
		logrus.Info("Getting charging control capability")
		capable, err := apiClient.GetChargingControlCapable()
		if err != nil {
			logrus.WithError(err).Warnf("Failed to get charging capablility")
			ctrl.toggleMenusRequiringInstall(true, false, false)
			return cleanupFunc
		}
		logrus.WithField("capable", capable).Info("Got charging control capability")
		logrus.Info("Getting daemon version")
		daemonVersion, err := apiClient.GetVersion()
		if err != nil {
			logrus.WithError(err).Warnf("Failed to get version")
			ctrl.toggleMenusRequiringInstall(true, capable, true)
		} else {
			ctrl.toggleMenusRequiringInstall(true, capable, daemonVersion != version.Version)
		}
		logrus.WithField("daemonVersion", daemonVersion).WithField("clientVersion", version.Version).Info("Got daemon")
	}

	return cleanupFunc
}

func showAlert(msg, body string) {
	alert := appkit.NewAlert()
	alert.SetIcon(appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("exclamationmark.triangle", "s"))
	alert.SetAlertStyle(appkit.AlertStyleWarning)
	alert.SetMessageText(msg)
	alert.SetInformativeText(body)
	alert.RunModal()
}

func setMenubarImage(menubarStatusItem appkit.StatusItem, daemonInstalled, capable, needUpgrade bool) {
	if !daemonInstalled {
		menubarStatusItem.Button().SetImage(appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("batteryblock.slash", "batt daemon not installed"))
		return
	}
	if !capable {
		menubarStatusItem.Button().SetImage(appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("minus.plus.batteryblock.exclamationmark", "You machine cannot run batt"))
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
	installItem         appkit.MenuItem
	upgradeItem         appkit.MenuItem
	stateItem           appkit.MenuItem
	currentLimitItem    appkit.MenuItem
	quickLimitsItem     appkit.MenuItem
	setQuickLimitsItems map[int]appkit.MenuItem

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
	for _, it := range c.setQuickLimitsItems {
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
	for limit, item := range c.setQuickLimitsItems {
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
	attrStr.AddAttributeValueRange(foundation.AttributedStringKey("NSFont"), font, foundation.Range{Location: 0, Length: uint64(len(fullString))})
	return attrStr.AttributedString
}
