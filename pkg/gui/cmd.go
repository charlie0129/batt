package gui

import (
	"fmt"
	"os"

	"github.com/distatus/battery"
	"github.com/fatih/color"
	pkgerrors "github.com/pkg/errors"
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
	"github.com/progrium/darwinkit/objc"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/client"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/version"
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
	delegate := &appkit.ApplicationDelegate{}
	delegate.SetApplicationDidFinishLaunching(func(notification foundation.Notification) {
		logrus.WithField("version", version.Version).WithField("gitCommit", version.GitCommit).Info("batt gui")
		addMenubar(app, apiClient)
	})

	app.SetDelegate(delegate)
	app.Run()
}

//nolint:gocyclo
func addMenubar(app appkit.Application, apiClient *client.Client) {
	menubarIcon := appkit.StatusBar_SystemStatusBar().StatusItemWithLength(appkit.VariableStatusItemLength)
	objc.Retain(&menubarIcon)
	setMenubarImage(menubarIcon, false, false, false)
	menu := appkit.NewMenuWithTitle("batt")
	menu.SetAutoenablesItems(false)

	// ==================== INSTALL & STATES ====================

	unintallOrUpgrade := func(sender objc.Object) {
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

	upgradeItem := appkit.NewMenuItemWithAction("Upgrade Daemon...", "u", unintallOrUpgrade)
	upgradeItem.SetToolTip(`Your batt daemon is not compatible with this client version and needs to be upgraded. This is usually caused by a new client version that requires a new daemon version. You can upgrade the batt daemon by running this command.`)
	menu.AddItem(upgradeItem)

	installItem := appkit.NewMenuItemWithAction("Install Daemon...", "i", unintallOrUpgrade)
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

	controlMagSafeLEDItem := checkBoxItem("Control MagSafe LED", "", func(checked bool) {
		// Perform action based on new state
		_, err := apiClient.SetControlMagSafeLED(checked)
		if err != nil {
			logrus.WithError(err).Error("Failed to set control mag safe LED")
			showAlert("Failed to set MagSafe LED control", err.Error())
			return
		}
	})
	controlMagSafeLEDItem.SetToolTip(`This option can make the MagSafe LED on your MacBook change color according to the charging status. For example:

- Green: Charge limit is reached and charging is stopped.
- Orange: Charging is in progress.
- Off: Just woken up from sleep, charging is disabled and batt is waiting before controlling charging.

Note that you must have a MagSafe LED on your MacBook to use this feature.`)
	advancedMenu.AddItem(controlMagSafeLEDItem)

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
	menu.AddItem(disableItem)
	// Quit
	quitItem := appkit.NewMenuItemWithAction("Quit", "q", func(sender objc.Object) {
		logrus.Info("Quitting client")
		app.Terminate(nil)
	})
	quitItem.SetToolTip("Quit the batt client. Note that this does not stop the batt daemon, which will continue to run in the background. To stop the batt daemon, you can use the \"Disable Charging Limit\" command.")
	menu.AddItem(quitItem)

	menubarIcon.SetMenu(menu)

	// ==================== CALLBACKS ====================

	//nolint:staticcheck // the boolean conditions are intentionally written this way
	toggleMenusRequiringInstall := func(battInstalled bool, capable, needUpgrade bool) {
		if v := os.Getenv("BATT_GUI_NO_COMPATIBILITY_CHECK"); v == "1" || v == "true" {
			return
		}

		setMenubarImage(menubarIcon, battInstalled, capable, needUpgrade)

		installItem.SetHidden(!(!battInstalled))
		upgradeItem.SetHidden(!(battInstalled && (needUpgrade || !capable)))
		stateItem.SetHidden(!(battInstalled && capable))
		currentLimitItem.SetHidden(!(battInstalled && capable))

		quickLimitsItem.SetHidden(!((battInstalled && capable) && !needUpgrade))
		for _, quickLimitItem := range setQuickLimitsItems {
			quickLimitItem.SetHidden(!((battInstalled && capable) && !needUpgrade))
		}

		advancedSubMenuItem.SetHidden(!battInstalled)
		controlMagSafeLEDItem.SetHidden(!((battInstalled && capable) && !needUpgrade))
		preventIdleSleepItem.SetHidden(!((battInstalled && capable) && !needUpgrade))
		disableChargingPreSleepItem.SetHidden(!((battInstalled && capable) && !needUpgrade))
		preventSystemSleepItem.SetHidden(!((battInstalled && capable) && !needUpgrade))
		forceDischargeItem.SetHidden(!((battInstalled && capable) && !needUpgrade))
		uninstallItem.SetHidden(!battInstalled)

		disableItem.SetHidden(!((battInstalled && capable) && !needUpgrade))
	}

	menuDelegate := &appkit.MenuDelegate{}

	menuDelegate.SetMenuWillOpen(func(menu appkit.Menu) {
		// Get current information from the API
		rawConfig, err := apiClient.GetConfig()
		if err != nil {
			logrus.WithError(err).Error("Failed to get config")
			toggleMenusRequiringInstall(false, false, false)
			return
		}
		capable, err := apiClient.GetChargingControlCapable()
		if err != nil {
			logrus.WithError(err).Error("Failed to get charging capablility")
			toggleMenusRequiringInstall(true, false, false)
			return
		}
		daemonVersion, err := apiClient.GetVersion()
		if err != nil {
			logrus.WithError(err).Error("Failed to get version")
			toggleMenusRequiringInstall(true, capable, true)
		} else {
			toggleMenusRequiringInstall(true, capable, daemonVersion != version.Version)
		}
		logrus.WithField("daemonVersion", daemonVersion).WithField("clientVersion", version.Version).Info("Got daemon")

		isCharging, err := apiClient.GetCharging()
		if err != nil {
			logrus.WithError(err).Error("Failed to get charging state")
			stateItem.SetTitle("State: Error")
			return
		}
		isPluggedIn, err := apiClient.GetPluggedIn()
		if err != nil {
			logrus.WithError(err).Error("Failed to get plugged in state")
			stateItem.SetTitle("State: Error")
			return
		}
		currentCharge, err := apiClient.GetCurrentCharge()
		if err != nil {
			logrus.WithError(err).Error("Failed to get current charge")
			stateItem.SetTitle("State: Error")
			return
		}
		batteryInfo, err := apiClient.GetBatteryInfo()
		if err != nil {
			logrus.WithError(err).Error("Failed to get battery info")
			stateItem.SetTitle("State: Error")
			return
		}

		conf := config.NewFileFromConfig(rawConfig, "")
		logrus.WithFields(conf.LogrusFields()).Info("Got config")

		currentLimitItem.SetTitle(fmt.Sprintf("Current Limit: %d%%", conf.UpperLimit()))
		for limit, quickLimitItem := range setQuickLimitsItems {
			setCheckboxItem(quickLimitItem, limit == conf.UpperLimit())
		}

		state := "Not Charging"
		switch batteryInfo.State {
		case battery.Charging:
			state = color.GreenString("Charging")
		case battery.Discharging:
			state = color.RedString("Discharging")
		case battery.Full:
			state = "Full"
		}
		stateItem.SetTitle("State: " + state)

		if !isCharging && isPluggedIn && conf.UpperLimit() < 100 && currentCharge < conf.LowerLimit() {
			stateItem.SetTitle("State: Will Charge Soon")
		}

		setCheckboxItem(controlMagSafeLEDItem, conf.ControlMagSafeLED())
		setCheckboxItem(preventIdleSleepItem, conf.PreventIdleSleep())
		setCheckboxItem(disableChargingPreSleepItem, conf.DisableChargingPreSleep())
		setCheckboxItem(preventSystemSleepItem, conf.PreventSystemSleep())
		if adapter, err := apiClient.GetAdapter(); err == nil {
			setCheckboxItem(forceDischargeItem, !adapter)
		} else {
			logrus.WithError(err).Error("Failed to get adapter")
			forceDischargeItem.SetEnabled(false)
		}
	})
	menu.SetDelegate(menuDelegate)

	// Update icon onstart up
	{
		logrus.Info("Getting config")
		rawConfig, err := apiClient.GetConfig()
		if err != nil {
			logrus.WithError(err).Warnf("Failed to get config")
			toggleMenusRequiringInstall(false, false, false)
			return
		}
		conf := config.NewFileFromConfig(rawConfig, "")
		logrus.WithFields(conf.LogrusFields()).Info("Got config")
		logrus.Info("Getting charging control capability")
		capable, err := apiClient.GetChargingControlCapable()
		if err != nil {
			logrus.WithError(err).Warnf("Failed to get charging capablility")
			toggleMenusRequiringInstall(true, false, false)
			return
		}
		logrus.WithField("capable", capable).Info("Got charging control capability")
		logrus.Info("Getting daemon version")
		daemonVersion, err := apiClient.GetVersion()
		if err != nil {
			logrus.WithError(err).Warnf("Failed to get version")
			toggleMenusRequiringInstall(true, capable, true)
		} else {
			toggleMenusRequiringInstall(true, capable, daemonVersion != version.Version)
		}
		logrus.WithField("daemonVersion", daemonVersion).WithField("clientVersion", version.Version).Info("Got daemon")
	}
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
