package gui

import (
	"fmt"
	"os"
	"runtime/debug"

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

This command should not be called directly by user. Users should use the .app bundle to start the GUI.`,
		Run: func(_ *cobra.Command, _ []string) {
			Run(unixSocketPath)
		},
	}

	return cmd
}

func Run(unixSocketPath string) {
	debug.PrintStack()

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
			showAlert("Failed to get executable path", err.Error())
			return
		}

		err = installDaemon(exe)
		if err != nil {
			showAlert("Installation failed", err.Error())
			return
		}

		err = startAppAtBoot()
		if err != nil {
			showAlert("Failed to start app at boot", err.Error())
			return
		}

		setMenubarImage(menubarIcon, true, true, false)
	}

	upgradeItem := appkit.NewMenuItemWithAction("Upgrade Daemon...", "u", unintallOrUpgrade)
	menu.AddItem(upgradeItem)

	installItem := appkit.NewMenuItemWithAction("Install Daemon...", "i", unintallOrUpgrade)
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
			showAlert("Failed to set MagSafe LED control", err.Error())
			return
		}
	})
	advancedMenu.AddItem(controlMagSafeLEDItem)

	preventIdleSleepItem := checkBoxItem("Prevent Idle Sleep when Charging", "", func(checked bool) {
		// Perform action based on new state
		_, err := apiClient.SetPreventIdleSleep(checked)
		if err != nil {
			showAlert("Failed to set prevent idle sleep", err.Error())
			return
		}
	})
	advancedMenu.AddItem(preventIdleSleepItem)

	disableChargingPreSleepItem := checkBoxItem("Disable Charging before Sleep", "", func(checked bool) {
		// Perform action based on new state
		_, err := apiClient.SetDisableChargingPreSleep(checked)
		if err != nil {
			showAlert("Failed to set prevent idle sleep", err.Error())
			return
		}
	})
	advancedMenu.AddItem(disableChargingPreSleepItem)

	preventSystemSleepItem := checkBoxItem("Prevent System Sleep when Charging", "", func(checked bool) {
		// Perform action based on new state
		_, err := apiClient.SetPreventSystemSleep(checked)
		if err != nil {
			showAlert("Failed to set prevent system sleep", err.Error())
			return
		}
	})
	advancedMenu.AddItem(preventSystemSleepItem)

	advancedMenu.AddItem(appkit.MenuItem_SeparatorItem())

	versionItem := appkit.NewMenuItemWithAction("Version: "+version.Version, "", func(sender objc.Object) {})
	versionItem.SetEnabled(false)
	advancedMenu.AddItem(versionItem)

	uninstallItem := appkit.NewMenuItemWithAction("Uninstall Daemon...", "", func(sender objc.Object) {
		exe, err := os.Executable()
		if err != nil {
			showAlert("Failed to get executable path", err.Error())
			return
		}

		err = uninstallDaemon(exe)
		if err != nil {
			showAlert("Failed to uninstall daemon", err.Error())
			return
		}

		err = UnregisterLoginItem()
		if err != nil {
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
	menu.AddItem(quitItem)

	menubarIcon.SetMenu(menu)

	// ==================== CALLBACKS ====================

	//nolint:staticcheck // the boolean conditions are intentionally written this way
	toggleMenusRequiringInstall := func(battInstalled bool, capable, needUpgrade bool) {
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
		uninstallItem.SetHidden(!battInstalled)

		disableItem.SetHidden(!((battInstalled && capable) && !needUpgrade))
	}

	menuDelegate := &appkit.MenuDelegate{}

	menuDelegate.SetMenuWillOpen(func(menu appkit.Menu) {
		// Get current information from the API
		rawConfig, err := apiClient.GetConfig()
		if err != nil {
			logrus.WithError(err).Warnf("Failed to get config")
			toggleMenusRequiringInstall(false, false, false)
			return
		}
		capable, err := apiClient.GetChargingControlCapable()
		if err != nil {
			logrus.WithError(err).Warnf("Failed to get charging capablility")
			toggleMenusRequiringInstall(true, false, false)
			return
		}
		daemonVersion, err := apiClient.GetVersion()
		if err != nil {
			logrus.WithError(err).Warnf("Failed to get version")
			toggleMenusRequiringInstall(true, capable, true)
		} else {
			toggleMenusRequiringInstall(true, capable, daemonVersion != version.Version)
		}
		logrus.WithField("daemonVersion", daemonVersion).WithField("clientVersion", version.Version).Info("Got daemon")

		isCharging, err := apiClient.GetCharging()
		if err != nil {
			stateItem.SetTitle("State: Error")
			return
		}
		isPluggedIn, err := apiClient.GetPluggedIn()
		if err != nil {
			stateItem.SetTitle("State: Error")
			return
		}
		currentCharge, err := apiClient.GetCurrentCharge()
		if err != nil {
			stateItem.SetTitle("State: Error")
			return
		}

		conf := config.NewFileFromConfig(rawConfig, "")
		logrus.WithFields(conf.LogrusFields()).Info("Got config")

		currentLimitItem.SetTitle(fmt.Sprintf("Current Limit: %d%%", conf.UpperLimit()))
		for limit, quickLimitItem := range setQuickLimitsItems {
			setCheckboxItem(quickLimitItem, limit == conf.UpperLimit())
		}

		if isCharging {
			// Charging
			if isPluggedIn {
				stateItem.SetTitle("State: Charging")
			} else {
				stateItem.SetTitle("State: Will Charge if Plugged in")
			}
		} else {
			// Not charging
			if conf.UpperLimit() < 100 { // Limit enabled
				stateItem.SetTitle("State: Not Charging")
				if isPluggedIn && currentCharge < conf.LowerLimit() {
					stateItem.SetTitle("State: Will Charge Soon")
				}
			} else { // Limit disabled
				stateItem.SetTitle("State: Limit Disabled")
			}
		}

		setCheckboxItem(controlMagSafeLEDItem, conf.ControlMagSafeLED())
		setCheckboxItem(preventIdleSleepItem, conf.PreventIdleSleep())
		setCheckboxItem(disableChargingPreSleepItem, conf.DisableChargingPreSleep())
		setCheckboxItem(preventSystemSleepItem, conf.PreventSystemSleep())
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
	alert.SetMessageText(msg)
	alert.SetInformativeText(body)
	alert.RunModal()
}

func setMenubarImage(menubarStatusItem appkit.StatusItem, daemonInstalled, capable, needUpgrade bool) {
	if !daemonInstalled {
		menubarStatusItem.Button().SetImage(appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("batteryblock.slash", "batt daemin not installed"))
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
