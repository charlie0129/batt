package main

import (
	"github.com/spf13/cobra"
)

func NewSetPreventIdleSleepCommand() *cobra.Command {
	return newEnableDisableCommand(
		"prevent-idle-sleep",
		"Set whether to prevent idle sleep during a charging session",
		`Set whether to prevent idle sleep during a charging session.

Due to macOS limitations, batt will be paused when your computer goes to sleep. As a result, when you are in a charging session and your computer goes to sleep, there is no way for batt to stop charging (since batt is paused by macOS) and the battery will charge to 100%. This option, together with disable-charging-pre-sleep, will prevent this from happening.

This option tells macOS NOT to go to sleep when the computer is in a charging session, so batt can continue to work until charging is finished. Note that it will only prevent **idle** sleep, when 1) charging is active 2) battery charge limit is enabled. So your computer can go to sleep as soon as a charging session is completed.

However, this options does not prevent manual sleep (limitation of macOS). For example, if you manually put your computer to sleep (by choosing the Sleep option in the top-left Apple menu) or close the lid, batt will still be paused and the issue mentioned above will still happen. This is where disable-charging-pre-sleep comes in.`,
		func() (string, error) { return apiClient.SetPreventIdleSleep(true) },
		func() (string, error) { return apiClient.SetPreventIdleSleep(false) },
	)
}

func NewSetDisableChargingPreSleepCommand() *cobra.Command {
	return newEnableDisableCommand(
		"disable-charging-pre-sleep",
		"Set whether to disable charging before sleep if charge limit is enabled",
		`Set whether to disable charging before sleep if charge limit is enabled.

As described in preventing-idle-sleep, batt will be paused by macOS when your computer goes to sleep, and there is no way for batt to continue controlling battery charging. This option will disable charging just before sleep, so your computer will not overcharge during sleep, even if the battery charge is below the limit.`,
		func() (string, error) { return apiClient.SetDisableChargingPreSleep(true) },
		func() (string, error) { return apiClient.SetDisableChargingPreSleep(false) },
	)
}

func NewSetControlMagSafeLEDCommand() *cobra.Command {
	return newEnableDisableCommand(
		"magsafe-led",
		"Control MagSafe LED according to battery charging status",
		`This option can make the MagSafe LED on your MacBook change color according to the charging status. For example:

- Green: Charge limit is reached and charging is stopped.
- Orange: Charging is in progress.
- Off: Just woken up from sleep, charging is disabled and batt is waiting before controlling charging.

Note that you must have a MagSafe LED on your MacBook to use this feature.`,
		func() (string, error) { return apiClient.SetControlMagSafeLED(true) },
		func() (string, error) { return apiClient.SetControlMagSafeLED(false) },
	)
}
