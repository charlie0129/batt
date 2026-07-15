#import "native_internal.h"

static void SetTooltip(BattMenuController *controller, BattMenuItem item, NSString *tooltip) {
    [controller item:item].toolTip = tooltip;
}

void BattApplyTooltips(BattMenuController *controller) {
    SetTooltip(controller, BattItemPowerFlow,
        @"Power flow data is updated every 60 seconds.");
    SetTooltip(controller, BattItemUpgrade,
        @"Your batt daemon is not compatible with this client version and needs to be upgraded. This is usually caused by a new client version that requires a new daemon version. You can upgrade the batt daemon by running this command.");
    SetTooltip(controller, BattItemInstall,
        @"Install the batt daemon. batt daemon is a component that controls charging. You must enter your password to install it because controlling charging is a privileged action.");
    SetTooltip(controller, BattItemMagSafe,
        @"Let batt control MagSafe LED to reflect the charging state of your MacBook (or force it off).\n\n"
         "Note that you must have a MagSafe LED on your MacBook to use this feature.");
    SetTooltip(controller, BattItemMagSafeEnabled,
        @"Enable MagSafe LED control. The LED will reflect charging status:\n\n"
         "- Green: Charge limit is reached and charging is stopped.\n"
         "- Orange: Charging is in progress.\n"
         "- Off: Woke from sleep, charging is off and batt is awaiting control.");
    SetTooltip(controller, BattItemMagSafeDisabled,
        @"Disable MagSafe LED control. The LED will stay in its default state (mostly orange).");
    SetTooltip(controller, BattItemMagSafeAlwaysOff,
        @"Force the MagSafe LED to stay off regardless of charging state.");
    SetTooltip(controller, BattItemPreventIdleSleep,
        @"Set whether to prevent idle sleep during a charging session.\n\n"
         "Due to macOS limitations, batt will be paused when your computer goes to sleep. As a result, when you are in a charging session and your computer goes to sleep, there is no way for batt to stop charging (since batt is paused by macOS) and the battery will charge to 100%. This option, together with \"Disable Charging before Sleep\", will prevent this from happening.\n\n"
         "This option tells macOS NOT to go to sleep when the computer is in a charging session, so batt can continue to work until charging is finished. Note that it will only prevent **idle** sleep, when 1) charging is active 2) battery charge limit is enabled. So your computer can go to sleep as soon as a charging session is completed.\n\n"
         "However, this options does not prevent manual sleep (limitation of macOS). For example, if you manually put your computer to sleep (by choosing the Sleep option in the top-left Apple menu) or close the lid, batt will still be paused and the issue mentioned above will still happen. This is where \"Disable Charging before Sleep\" comes in.");
    SetTooltip(controller, BattItemDisableChargingPreSleep,
        @"Set whether to disable charging before sleep if charge limit is enabled.\n\n"
         "As described in \"Prevent Idle Sleep when Charging\", batt will be paused by macOS when your computer goes to sleep, and there is no way for batt to continue controlling battery charging. This option will disable charging just before sleep, so your computer will not overcharge during sleep, even if the battery charge is below the limit.");
    SetTooltip(controller, BattItemPreventSystemSleep,
        @"Set whether to prevent system sleep during a charging session (experimental).\n\n"
         "This option tells macOS to create power assertion, which prevents sleep, when all conditions are met:\n\n"
         "1) charging is active\n"
         "2) battery charge limit is enabled\n"
         "3) computer is connected to charger.\n"
         "So your computer can go to sleep as soon as a charging session is completed / charger disconnected.\n\n"
         "Does similar thing to prevent-idle-sleep, but works for manual sleep too.\n\n"
         "Note: please disable disable-charging-pre-sleep and prevent-idle-sleep, while this feature is in use");
    SetTooltip(controller, BattItemForceDischarge,
        @"Cut power from the wall. This has the same effect as unplugging the power adapter, even if the adapter is physically plugged in.\n\n"
         "This is useful when you want to use your battery to lower the battery charge, but you don't want to unplug the power adapter.\n\n"
         "NOTE: if you are using Clamshell mode (using a Mac laptop with an external monitor and the lid closed), *cutting power will cause your Mac to go to sleep*. This is a limitation of macOS. There are ways to prevent this, but it is not recommended for most users.");
    SetTooltip(controller, BattItemAutoCalibration,
        @"Calibration helps you calibrate your battery by automatically discharging and charging it according to best practices.\n\n"
         "batt prevents idle sleep for the entire calibration session. Closing the lid or explicitly choosing Sleep can still force sleep.\n\n"
         "It's recommended to run the calibration process every few months.");
    SetTooltip(controller, BattItemUninstall,
        @"Uninstall the batt daemon. This will remove the batt daemon from your system. You must enter your password to uninstall it.\n\n"
         "After uninstalling the batt daemon, no charging control will be present on your system and your Mac will charge to 100% as normal. The menubar app will still be present, but all options will be disabled. You can remove the menubar app by moving it to the trash.");
    SetTooltip(controller, BattItemDisableLimit,
        @"Disable the battery charge limit and let your Mac charge to 100%, either indefinitely or for a selected duration. After a temporary disable, batt automatically restores your current limit.");
    SetTooltip(controller, BattItemQuit,
        @"Quit the batt menubar app, but keep the batt daemon running.\n\n"
         "Since the batt daemon is still running, batt can continue to control charging. This is useful if you don't want the menubar icon to show up, but still want to use batt. When the client is not running, you can change batt settings using the command line interface (batt). To prevent the menubar app from starting at login, you can remove it in System Settings -> General -> Login Items & Extensions -> remove batt.app from the list (do NOT remove the batt daemon).\n\n"
         "If you want to stop batt completely (menubar app and the daemon), you can use the \"Disable Charge Limit\" command. To uninstall, you can use the \"Uninstall Daemon\" command in the Advanced menu.");
}
