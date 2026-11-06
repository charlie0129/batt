package main

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/compatibility"
)

// NewChargeCommand returns the one-time charge commands. A one-time charge
// charges the battery once and then hands control straight back to the
// configured charge limit, which it never changes.
func NewChargeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "charge",
		Short:   "Charge once without changing the charge limit",
		GroupID: gBasic,
		Long: `Charge the battery once, then let the configured charge limit apply again.

Your charge limit and lower-limit delta stay as they are, so normal behavior resumes as soon as the battery reaches the target. A one-time charge survives a daemon restart or reboot. Cancel it with "batt charge cancel"; setting a limit with "batt limit" cancels it too. Native mode does not support "batt charge now"; "batt charge full" remains available.`,
		Example: `  batt charge now
  batt charge full
  batt charge cancel`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "now",
			Short: "Charge to the configured limit now",
			Long: `Charge to the configured upper limit now.

In modes with a lower limit, this starts charging without waiting for the charge to fall below that threshold. The lower limit is not changed. Native mode does not support this action because macOS controls when charging resumes. Use "batt charge full" instead.`,
			Args: cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				return runChargeOnce(
					apiClient.ChargeOnceToLimit,
					"failed to start charging to the charge limit",
					"successfully started charging to the charge limit",
				)
			},
		},
		&cobra.Command{
			Use:   "full",
			Short: "Charge to 100% once",
			Long: `Charge to 100% once.

Your charge limit is restored automatically as soon as the battery reaches 100%.`,
			Args: cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				return runChargeOnce(
					apiClient.ChargeOnceToFull,
					"failed to start charging to 100%",
					"successfully started charging to 100%. The charge limit will be restored automatically",
				)
			},
		},
		&cobra.Command{
			Use:   "cancel",
			Short: "Cancel the one-time charge",
			Long: `Cancel a running one-time charge.

The configured charge limit applies again immediately. In native mode, the battery can take a minute or more to respond.`,
			Args: cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				return runChargeOnce(
					apiClient.CancelChargeOnce,
					"failed to cancel the one-time charge",
					"successfully cancelled the one-time charge",
				)
			},
		},
	)

	return annotateCapability(cmd, compatibility.FeatureChargingControl)
}

func runChargeOnce(call func() (string, error), failure, success string) error {
	ret, err := call()
	if err != nil {
		return fmt.Errorf("%s: %v", failure, err)
	}

	if ret != "" {
		slog.Info(fmt.Sprintf("daemon responded: %s", ret))
	}

	slog.Info(success)

	return nil
}
