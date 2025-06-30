package main

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func NewSetPreventIdleSleepCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "prevent-idle-sleep",
		Short:   "Set whether to prevent idle sleep during a charging session",
		GroupID: gAdvanced,
		Long: `Set whether to prevent idle sleep during a charging session.

Due to macOS limitations, batt will be paused when your computer goes to sleep. As a result, when you are in a charging session and your computer goes to sleep, there is no way for batt to stop charging (since batt is paused by macOS) and the battery will charge to 100%. This option, together with disable-charging-pre-sleep, will prevent this from happening.

This option tells macOS NOT to go to sleep when the computer is in a charging session, so batt can continue to work until charging is finished. Note that it will only prevent **idle** sleep, when 1) charging is active 2) battery charge limit is enabled. So your computer can go to sleep as soon as a charging session is completed.

However, this options does not prevent manual sleep (limitation of macOS). For example, if you manually put your computer to sleep (by choosing the Sleep option in the top-left Apple menu) or close the lid, batt will still be paused and the issue mentioned above will still happen. This is where disable-charging-pre-sleep comes in.`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Prevent idle sleep during a charging session",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.SetPreventIdleSleep(true)
				if err != nil {
					return fmt.Errorf("failed to set prevent idle sleep: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully enabled idle sleep prevention")

				return nil
			},
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Do not prevent idle sleep during a charging session",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.SetPreventIdleSleep(false)
				if err != nil {
					return fmt.Errorf("failed to set prevent idle sleep: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully disabled idle sleep prevention")

				return nil
			},
		},
	)

	return cmd
}

func NewSetDisableChargingPreSleepCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "disable-charging-pre-sleep",
		Short:   "Set whether to disable charging before sleep if charge limit is enabled",
		GroupID: gAdvanced,
		Long: `Set whether to disable charging before sleep if charge limit is enabled.

As described in preventing-idle-sleep, batt will be paused by macOS when your computer goes to sleep, and there is no way for batt to continue controlling battery charging. This option will disable charging just before sleep, so your computer will not overcharge during sleep, even if the battery charge is below the limit.`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Disable charging before sleep during a charging session",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.SetDisableChargingPreSleep(true)
				if err != nil {
					return fmt.Errorf("failed to set disable charging pre sleep: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully enabled disable-charging-pre-sleep")

				return nil
			},
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Do not disable charging before sleep during a charging session",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.SetDisableChargingPreSleep(false)
				if err != nil {
					return fmt.Errorf("failed to set disable charging pre sleep: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully disabled disable-charging-pre-sleep")

				return nil
			},
		},
	)

	return cmd
}

func NewSetPreventSystemSleepCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "prevent-system-sleep",
		Short:   "Set whether to prevent system sleep during a charging session",
		GroupID: gAdvanced,
		Long:    `TODO - explain`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Prevent system sleep during a charging session",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.SetPreventSystemSleep(true)
				if err != nil {
					return fmt.Errorf("failed to set prevent system sleep: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully enabled system sleep prevention")

				return nil
			},
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Do not prevent system sleep during a charging session",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.SetPreventSystemSleep(false)
				if err != nil {
					return fmt.Errorf("failed to set prevent system sleep: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully disabled system sleep prevention")

				return nil
			},
		},
	)

	return cmd
}

func NewSetControlMagSafeLEDCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "magsafe-led",
		Short:   "Control MagSafe LED according to battery charging status",
		GroupID: gAdvanced,
		Long: `This option can make the MagSafe LED on your MacBook change color according to the charging status. For example:

- Green: charge limit is reached and charging is stopped.
- Orange: charging is in progress.
- Off: just woken up from sleep, charing is disabled and batt is waiting before controlling charging.

Note that you must have a MagSafe LED on your MacBook to use this feature.`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Control MagSafe LED according to battery charging status",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.SetControlMagSafeLED(true)
				if err != nil {
					return fmt.Errorf("failed to set magsafe: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully enabled magsafe led controlling")

				return nil
			},
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Do not control MagSafe LED",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.SetControlMagSafeLED(false)
				if err != nil {
					return fmt.Errorf("failed to set magsafe: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully disabled magsafe led controlling")

				return nil
			},
		},
	)

	return cmd
}
