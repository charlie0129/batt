package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var extendedDurationRe = regexp.MustCompile(`^(\d+)([dw])$`)

// parseDuration parses a duration like time.ParseDuration, additionally
// accepting whole days ("1d") and weeks ("2w").
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)

	if m := extendedDurationRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %v", s, err)
		}
		unit := 24 * time.Hour
		if m[2] == "w" {
			unit = 7 * 24 * time.Hour
		}
		return time.Duration(n) * unit, nil
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: use a value like 30m, 2h, 1d or 1w", s)
	}

	return d, nil
}

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

// formatRestoreDelay renders a countdown at minute granularity.
func formatRestoreDelay(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}

	return d.Round(time.Minute).String()
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
