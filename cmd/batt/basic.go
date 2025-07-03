package main

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/version"
)

func NewVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Printf("%s %s\n", version.Version, version.GitCommit)
		},
	}
}

func NewLimitCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "limit [percentage]",
		Short:   "Set upper charge limit",
		GroupID: gBasic,
		Long: `Set upper charge limit.

This is a percentage from 10 to 100.

Setting the limit to 10-99 will enable the battery charge limit. However, setting the limit to 100 will disable the battery charge limit, which is the default behavior of macOS.`,
		RunE: func(_ *cobra.Command, args []string) error {
			limit, err := parseIntArg(args, "limit")
			if err != nil {
				return err
			}

			ret, err := apiClient.SetLimit(limit)
			if err != nil {
				return fmt.Errorf("failed to set limit: %v", err)
			}

			if ret != "" {
				logrus.Infof("daemon responded: %s", ret)
			}

			logrus.Infof("successfully set battery charge limit to %d%%", limit)

			return nil
		},
	}
}

func NewDisableCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "disable",
		Short:   "Disable batt",
		GroupID: gBasic,
		Long: `Disable batt.

Stop batt from controlling battery charging. This will allow your Mac to charge to 100%.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			ret, err := apiClient.SetLimit(100)
			if err != nil {
				return fmt.Errorf("failed to disable batt: %v", err)
			}

			if ret != "" {
				logrus.Infof("daemon responded: %s", ret)
			}

			logrus.Infof("successfully disabled batt. Charge limit has been reset to 100%%. To re-enable batt, just set a charge limit using \"batt limit\".")

			return nil
		},
	}
}

func NewAdapterCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "adapter",
		Short:   "Enable or disable power input",
		GroupID: gBasic,
		Long: `Cut or restore power from the wall. This has the same effect as unplugging/plugging the power adapter, even if the adapter is physically plugged in.

This is useful when you want to use your battery to lower the battery charge, but you don't want to unplug the power adapter.

NOTE: if you are using Clamshell mode (using a Mac laptop with an external monitor and the lid closed), *cutting power will cause your Mac to go to sleep*. This is a limitation of macOS. There are ways to prevent this, but it is not recommended for most users.`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "disable",
			Short: "Disable power adapter",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.SetAdapter(false)
				if err != nil {
					return fmt.Errorf("failed to disable power adapter: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully disabled power adapter")

				return nil
			},
		},
		&cobra.Command{
			Use:   "enable",
			Short: "Enable power adapter",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.SetAdapter(true)
				if err != nil {
					return fmt.Errorf("failed to enable power adapter: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully enabled power adapter")

				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Get the current status of power adapter",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.GetAdapter()
				if err != nil {
					return fmt.Errorf("failed to get power adapter status: %v", err)
				}

				if ret {
					logrus.Infof("power adapter is enabled")
				} else {
					logrus.Infof("power adapter is disabled")
				}

				return nil
			},
		},
	)

	return cmd
}

func NewLowerLimitDeltaCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "lower-limit-delta",
		Short:   "Set the delta between lower and upper charge limit",
		GroupID: gAdvanced,
		Long: `Set the delta between lower and upper charge limit.

When you set a charge limit, for example, on a Lenovo ThinkPad, you can set two percentages. The first one is the upper limit, and the second one is the lower limit. When the battery charge is above the upper limit, the computer will stop charging. When the battery charge is below the lower limit, the computer will start charging. If the battery charge is between the two limits, the computer will keep whatever charging state it is in.

batt have similar features. The charge limit you have set (using 'batt limit') will be used as the upper limit. By default, The lower limit will be set to 2% less than the upper limit. Same as using 'batt lower-limit-delta 2'. To customize the lower limit, use 'batt lower-limit-delta'.

For example, if you want to set the lower limit to be 5% less than the upper limit, run 'sudo batt lower-limit-delta 5'. By doing this, if you have your charge (upper) limit set to 60%, the lower limit will be 55%.`,
		RunE: func(_ *cobra.Command, args []string) error {
			delta, err := parseIntArg(args, "delta")
			if err != nil {
				return err
			}

			ret, err := apiClient.SetLowerLimitDelta(delta)
			if err != nil {
				return fmt.Errorf("failed to set lower limit delta: %v", err)
			}

			if ret != "" {
				logrus.Infof("daemon responded: %s", ret)
			}

			logrus.Infof("successfully set lower limit delta to %d%%", delta)

			return nil
		},
	}

	return cmd
}
