package gui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	pkgerrors "github.com/pkg/errors"
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
	"github.com/progrium/darwinkit/objc"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/client"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/version"
)

var (
	battSymlinkLocation = "/usr/local/bin/batt"
)

func NewGUICommand(unixSocketPath string) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "gui",
		Short:  "Start the batt GUI",
		Hidden: true,
		Long: `Start the batt GUI.

The GUI should be started by launchd. This command is for testing purposes only and should not be called by end users.`,
		Run: func(_ *cobra.Command, _ []string) {
			Run(unixSocketPath)
		},
	}

	return cmd
}

func Run(unixSocketPath string) {
	runtime.LockOSThread()

	apiClient := client.NewClient(unixSocketPath)

	app := appkit.Application_SharedApplication()
	delegate := &appkit.ApplicationDelegate{}
	delegate.SetApplicationDidFinishLaunching(func(notification foundation.Notification) {
		addMenubar(app, apiClient)
	})
	delegate.SetApplicationWillFinishLaunching(func(foundation.Notification) {

	})

	app.SetDelegate(delegate)
	app.Run()
}

func installDaemon(exe string) error {
	output := &bytes.Buffer{}
	cmd := exec.Command("/usr/bin/osascript", "-e", fmt.Sprintf("do shell script \"%s install --allow-non-root-access && /bin/ln -sf %s %s\" with administrator privileges", exe, exe, battSymlinkLocation))
	cmd.Stderr = output
	cmd.Stdout = output
	err := cmd.Run()
	if err != nil {
		return pkgerrors.Wrapf(err, "failed to install batt daemon: %s", output.String())
	}

	return nil
}

func startAppAtBoot(exe string) error {
	// tmpl := strings.ReplaceAll(hack.LaunchAgentPlistTemplate, "/path/to/batt", exe)
	//
	// home, err := os.UserHomeDir()
	// if err != nil {
	// 	return pkgerrors.Wrapf(err, "failed to get home directory")
	// }
	// launchAgentsDir := filepath.Join(home, "Library/LaunchAgents")
	//
	// // mkdir -p
	// err = os.MkdirAll(launchAgentsDir, 0755)
	// if err != nil {
	// 	return pkgerrors.Wrapf(err, "failed to create launch agent directory")
	// }
	//
	// err = os.WriteFile(path.Join(launchAgentsDir, "cc.chlc.battapp.plist"), []byte(tmpl), 0644)
	// if err != nil {
	// 	return pkgerrors.Wrapf(err, "failed to create launch agent plist")
	// }
	//
	// return nil

}

func addMenubar(app appkit.Application, apiClient *client.Client) {
	// TODO: prevent homebrew version from using it
	item := appkit.StatusBar_SystemStatusBar().StatusItemWithLength(appkit.VariableStatusItemLength)
	objc.Retain(&item)
	item.Button().SetImage(getMenubarImage(false, false, false))

	capable, err := apiClient.GetChargingControlCapable()
	if err != nil {
		item.Button().SetImage(getMenubarImage(false, false, false))
	} else {
		daemonVersion, err := apiClient.GetVersion()
		if err != nil || daemonVersion != version.Version {
			item.Button().SetImage(getMenubarImage(true, capable, true))
		}
	}

	menu := appkit.NewMenuWithTitle("batt")
	menu.SetAutoenablesItems(false)

	// ==================== INSTALL & STATES ====================

	upgradeItem := appkit.NewMenuItemWithAction("Upgrade daemon...", "u", func(sender objc.Object) {

	})
	menu.AddItem(upgradeItem)

	installItem := appkit.NewMenuItemWithAction("Install daemon...", "i", func(sender objc.Object) {
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

		err = startAppAtBoot(exe)
		if err != nil {
			showAlert("Failed to start app at boot", err.Error())
			return
		}

		item.Button().SetImage(getMenubarImage(true, true, false))
	})
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

	// TODO: move them into a for loop. I am too lazy to do it right now.
	set50Item := appkit.NewMenuItemWithAction("Set 50% Limit", "5", func(sender objc.Object) {
		ret, err := apiClient.SetLimit(50)
		if err != nil {
			showAlert("Failed to set limit", ret+err.Error())
			return
		}
	})
	menu.AddItem(set50Item)
	set60Item := appkit.NewMenuItemWithAction("Set 60% Limit", "6", func(sender objc.Object) {
		ret, err := apiClient.SetLimit(60)
		if err != nil {
			showAlert("Failed to set limit", ret+err.Error())
			return
		}
	})
	menu.AddItem(set60Item)
	set70Item := appkit.NewMenuItemWithAction("Set 70% Limit", "7", func(sender objc.Object) {
		ret, err := apiClient.SetLimit(70)
		if err != nil {
			showAlert("Failed to set limit", ret+err.Error())
			return
		}
	})
	menu.AddItem(set70Item)
	set80Item := appkit.NewMenuItemWithAction("Set 80% Limit", "8", func(sender objc.Object) {
		ret, err := apiClient.SetLimit(80)
		if err != nil {
			showAlert("Failed to set limit", ret+err.Error())
			return
		}
	})
	menu.AddItem(set80Item)
	set90Item := appkit.NewMenuItemWithAction("Set 90% Limit", "9", func(sender objc.Object) {
		ret, err := apiClient.SetLimit(90)
		if err != nil {
			showAlert("Failed to set limit", ret+err.Error())
			return
		}
	})
	menu.AddItem(set90Item)

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

	preventIdleSleepItem := checkBoxItem("Prevent idle sleep", "", func(checked bool) {
		// Perform action based on new state
		_, err := apiClient.SetPreventIdleSleep(checked)
		if err != nil {
			showAlert("Failed to set prevent idle sleep", err.Error())
			return
		}
	})
	advancedMenu.AddItem(preventIdleSleepItem)

	disableChargingPreSleepItem := checkBoxItem("Disable charging before sleep", "", func(checked bool) {
		// Perform action based on new state
		_, err := apiClient.SetDisableChargingPreSleep(checked)
		if err != nil {
			showAlert("Failed to set prevent idle sleep", err.Error())
			return
		}
	})
	advancedMenu.AddItem(disableChargingPreSleepItem)

	advancedMenu.AddItem(appkit.MenuItem_SeparatorItem())

	uninstallItem := appkit.NewMenuItemWithAction("Uninstall daemon...", "", func(sender objc.Object) {
		exe, err := os.Executable()
		if err != nil {
			showAlert("Failed to get executable path", err.Error())
			return
		}

		output := &bytes.Buffer{}

		cmd := exec.Command("/usr/bin/osascript", "-e", fmt.Sprintf("do shell script \"%s uninstall && rm -f %s\" with administrator privileges", exe, battSymlinkLocation))
		cmd.Stderr = output
		cmd.Stdout = output
		err = cmd.Run()
		if err != nil {
			showAlert("Failed to uninstall daemon", output.String())
			return
		}

		item.Button().SetImage(getMenubarImage(false, true, false))

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
		app.Terminate(nil)
	})
	menu.AddItem(quitItem)

	item.SetMenu(menu)

	// ==================== CALLBACKS ====================

	toggleMenusRequiringInstall := func(battInstalled bool, capable, needUpgrade bool) {
		item.Button().SetImage(getMenubarImage(battInstalled, capable, needUpgrade))

		installItem.SetHidden(battInstalled)
		upgradeItem.SetHidden(!(needUpgrade && battInstalled))
		stateItem.SetHidden(!battInstalled)
		currentLimitItem.SetHidden(!battInstalled)

		quickLimitsItem.SetHidden(!(battInstalled && !needUpgrade))
		set50Item.SetHidden(!(battInstalled && !needUpgrade))
		set60Item.SetHidden(!(battInstalled && !needUpgrade))
		set70Item.SetHidden(!(battInstalled && !needUpgrade))
		set80Item.SetHidden(!(battInstalled && !needUpgrade))
		set90Item.SetHidden(!(battInstalled && !needUpgrade))

		advancedSubMenuItem.SetHidden(!battInstalled)
		controlMagSafeLEDItem.SetHidden(!(battInstalled && !needUpgrade))
		preventIdleSleepItem.SetHidden(!(battInstalled && !needUpgrade))
		disableChargingPreSleepItem.SetHidden(!(battInstalled && !needUpgrade))
		uninstallItem.SetHidden(!battInstalled)

		disableItem.SetHidden(!(battInstalled && !needUpgrade))
	}

	menuDelegate := &appkit.MenuDelegate{}

	menuDelegate.SetMenuWillOpen(func(menu appkit.Menu) {
		// Get current information from the API
		rawConfig, err := apiClient.GetConfig()
		if err != nil {
			toggleMenusRequiringInstall(false, false, false)
			return
		}
		capable, err := apiClient.GetChargingControlCapable()
		if err != nil {
			toggleMenusRequiringInstall(true, false, false)
			return
		}
		daemonVersion, err := apiClient.GetVersion()
		if err != nil {
			toggleMenusRequiringInstall(true, capable, true)
		} else {
			toggleMenusRequiringInstall(true, capable, daemonVersion != version.Version)
		}

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

		currentLimitItem.SetTitle(fmt.Sprintf("Current Limit: %d%%", conf.UpperLimit()))

		if isCharging {
			// Charging
			if isPluggedIn {
				stateItem.SetTitle("State: charging")
			} else {
				stateItem.SetTitle("State: will charge if plugged in")
			}
		} else {
			// Not charging
			if conf.UpperLimit() < 100 { // Limit enabled
				stateItem.SetTitle("State: not charging")
				if isPluggedIn && currentCharge < conf.LowerLimit() {
					stateItem.SetTitle("State: about to charge")
				}
			} else { // Limit disabled
				stateItem.SetTitle("State: full")
			}
		}

		setCheckboxItem(controlMagSafeLEDItem, conf.ControlMagSafeLED())
		setCheckboxItem(preventIdleSleepItem, conf.PreventIdleSleep())
		setCheckboxItem(disableChargingPreSleepItem, conf.DisableChargingPreSleep())
	})
	menu.SetDelegate(menuDelegate)
}

func showAlert(msg, body string) {
	alert := appkit.NewAlert()
	alert.SetIcon(appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("exclamationmark.triangle", "s"))
	alert.SetMessageText(msg)
	alert.SetInformativeText(body)
	alert.RunModal()
}

func getMenubarImage(installed, capable, needUpgrade bool) appkit.Image {
	if !installed {
		return appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("batteryblock.slash", "batt not installed")
	}
	if !capable {
		return appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("minus.plus.batteryblock.exclamationmark", "batt error")
	}
	if needUpgrade {
		return appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("fluid.batteryblock", "batt needs upgrade")
	}
	return appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("minus.plus.batteryblock", "batt icon")
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
