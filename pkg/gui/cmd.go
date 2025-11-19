package gui

import (
	"context"
	"fmt"
	"os"

	pkgerrors "github.com/pkg/errors"
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/objc"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/client"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/events"
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
	cleanup, ctrl := addMenubar(app, apiClient)
	defer cleanup()

	// Start SSE subscription for daemon events (calibration phase changes)
	go startEventBridge(apiClient, ctrl)

	app.Run()
}

// startEventBridge subscribes to client events and triggers UI refreshes on demand.
func startEventBridge(api *client.Client, ctrl *menuController) {
	ctx, cancel := context.WithCancel(context.Background())
	ctrl.eventCancel = cancel

	evCh := api.SubscribeEvents(ctx)

	for ev := range evCh {
		logrus.WithFields(logrus.Fields{
			"event": ev.Name,
			"data":  string(ev.Data),
		}).Debug("new event")

		if ev.Name == events.CalibrationAction {
			payload, err := events.DecodeAs[events.CalibrationActionEvent](ev)
			if err != nil {
				logrus.WithError(err).Error("failed to decode calibration.action event")
				continue
			}

			showNotification("Calibration", payload.Message)
		} else if ev.Name == events.CalibrationPhase {
			payload, err := events.DecodeAs[events.CalibrationPhaseEvent](ev)
			if err != nil {
				logrus.WithError(err).Error("failed to decode calibration.phase event")
				continue
			}

			toPhase := calibration.Phase(payload.To)
			switch toPhase {
			case calibration.PhaseDischarge:
				fallthrough
			case calibration.PhaseCharge:
				fallthrough
			case calibration.PhaseHold:
				fallthrough
			case calibration.PhasePostHold:
				fallthrough
			case calibration.PhaseRestore:
				fallthrough
			case calibration.PhaseError:
				showNotification("Calibration", payload.Message)
			}
		}
	}
}

//nolint:gocyclo
func addMenubar(app appkit.Application, apiClient *client.Client) (func(), *menuController) {
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
			alert.AddButtonWithTitle("Start")
			alert.AddButtonWithTitle("Cancel")
			response := alert.RunModal()
			if response != appkit.AlertFirstButtonReturn {
				logrus.Info("User cancelled force discharge")
				return
			}
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
	autoCalibrationSub.SetTitle("Auto Calibration...")
	autoCalibrationSub.SetToolTip(`This function helps you calibrate your battery by automatically discharging and charging it according to best practices.

Not recommended to run the calibration mode too frequently.`)
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
		alert.SetInformativeText(`This will:
1. Discharge to threshold, without sleep prevention.
2. Charge to 100%.
3. Hold at full for configured minutes.
4. Restore your original settings.

NOTES:
• You can pause or cancel anytime from the menu.
• Highly recommend keeping your Mac connected to power throughout the process to prevent the battery level from dropping below the threshold without timely charging.
• If you are using Clamshell mode (using a Mac laptop with an external monitor and the lid closed), *Discharging process will cause your Mac to go to sleep*. This is a limitation of macOS. There are ways to prevent this, but it is not recommended for most users.`)
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
		quickLimitsItems:            setQuickLimitsItems,
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
		if ctrl.eventCancel != nil {
			logrus.Debug("Cancelling event subscription")
			ctrl.eventCancel()
		}

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
			return cleanupFunc, ctrl
		}
		conf := config.NewFileFromConfig(rawConfig, "")
		logrus.WithFields(conf.LogrusFields()).Info("Got config")
		logrus.Info("Getting charging control capability")
		capable, err := apiClient.GetChargingControlCapable()
		if err != nil {
			logrus.WithError(err).Warnf("Failed to get charging capablility")
			ctrl.toggleMenusRequiringInstall(true, false, false)
			return cleanupFunc, ctrl
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

	return cleanupFunc, ctrl
}
