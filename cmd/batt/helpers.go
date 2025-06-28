package main

import (
	"fmt"
	"strconv"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func parseIntArg(args []string, valueName string) (int, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("invalid number of arguments")
	}

	value, err := strconv.Atoi(args[0])
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %v", valueName, err)
	}

	return value, nil
}

func newEnableDisableCommand(
	use, short, long string,
	enableFunc func() (string, error),
	disableFunc func() (string, error),
) *cobra.Command {
	cmd := &cobra.Command{
		Use:     use,
		Short:   short,
		Long:    long,
		GroupID: gAdvanced,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Enable " + short,
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := enableFunc()
				if err != nil {
					return fmt.Errorf("failed to enable %s: %v", use, err)
				}
				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}
				logrus.Infof("successfully enabled %s", use)
				return nil
			},
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Disable " + short,
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := disableFunc()
				if err != nil {
					return fmt.Errorf("failed to disable %s: %v", use, err)
				}
				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}
				logrus.Infof("successfully disabled %s", use)
				return nil
			},
		},
	)

	return cmd
}
