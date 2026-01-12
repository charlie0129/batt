package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/calibration"
)

func NewCalibrationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "calibration",
		Aliases: []string{"calibrate", "cali"},
		Short:   "Manage battery auto-calibration workflow",
		Long:    "Start, monitor, and control the multi-phase battery auto-calibration (discharge -> charge -> hold -> post-hold discharge -> restore).",
		GroupID: gAdvanced,
	}

	// start
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start a new calibration session using current config thresholds",
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := apiClient.StartCalibration()
			if err != nil {
				return fmt.Errorf("failed to start calibration: %w", err)
			}
			fmt.Println("Calibration started.")
			return nil
		},
	}

	// pause
	pauseCmd := &cobra.Command{
		Use:   "pause",
		Short: "Pause an in-progress calibration",
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := apiClient.PauseCalibration()
			if err != nil {
				return fmt.Errorf("failed to pause calibration: %w", err)
			}
			fmt.Println("Calibration paused.")
			return nil
		},
	}

	// resume
	resumeCmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume a paused calibration",
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := apiClient.ResumeCalibration()
			if err != nil {
				return fmt.Errorf("failed to resume calibration: %w", err)
			}
			fmt.Println("Calibration resumed.")
			return nil
		},
	}

	// cancel
	cancelCmd := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel (abort) the calibration and restore original limits",
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := apiClient.CancelCalibration()
			if err != nil {
				return fmt.Errorf("failed to cancel calibration: %w", err)
			}
			fmt.Println("Calibration canceled and state restored.")
			return nil
		},
	}

	// status
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show current calibration status",
		RunE: func(_ *cobra.Command, _ []string) error {
			tr, err := apiClient.GetTelemetry(false, true)
			if err != nil {
				return fmt.Errorf("failed to fetch calibration status: %w", err)
			}
			if tr.Calibration == nil {
				fmt.Println("No calibration data (idle or unavailable).")
				return nil
			}
			printCalibrationStatus(tr.Calibration)
			return nil
		},
	}

	// discharge-threshold
	dischargeThresholdCmd := &cobra.Command{
		Use:   "discharge-threshold <percentage>",
		Short: "Set the calibration discharge threshold percentage",
		Long: `Set the battery discharge threshold percentage for calibration.
The calibration will discharge the battery to this level before starting the charge phase.
Must be between 10 and 50 percent. Default is 15%.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var threshold int
			if _, err := fmt.Sscanf(args[0], "%d", &threshold); err != nil {
				return fmt.Errorf("invalid threshold: %w", err)
			}
			if threshold < 10 || threshold > 50 {
				return fmt.Errorf("threshold must be between 10 and 50, got %d", threshold)
			}
			msg, err := apiClient.SetCalibrationDischargeThreshold(threshold)
			if err != nil {
				return fmt.Errorf("failed to set discharge threshold: %w", err)
			}
			fmt.Println(msg)
			return nil
		},
	}

	// hold-duration
	holdDurationCmd := &cobra.Command{
		Use:   "hold-duration <minutes>",
		Short: "Set the calibration hold duration in minutes",
		Long: `Set the duration to hold at 100% charge during calibration.
Must be between 10 and 1440 minutes (24 hours). Default is 120 minutes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var minutes int
			if _, err := fmt.Sscanf(args[0], "%d", &minutes); err != nil {
				return fmt.Errorf("invalid duration: %w", err)
			}
			if minutes < 10 || minutes > 1440 {
				return fmt.Errorf("duration must be between 10 and 1440 minutes (24 hours), got %d", minutes)
			}
			msg, err := apiClient.SetCalibrationHoldDurationMinutes(minutes)
			if err != nil {
				return fmt.Errorf("failed to set hold duration: %w", err)
			}
			fmt.Println(msg)
			return nil
		},
	}

	cmd.AddCommand(startCmd, pauseCmd, resumeCmd, cancelCmd, statusCmd, dischargeThresholdCmd, holdDurationCmd)
	return cmd
}

func printCalibrationStatus(st *calibration.Status) {
	bold := func(format string, a ...interface{}) string { return color.New(color.Bold).Sprintf(format, a...) }
	fmt.Printf("Phase: %s\n", bold(string(st.Phase)))
	fmt.Printf("Charge: %s\n", bold("%d%%", st.ChargePercent))
	fmt.Printf("Plugged In: %v\n", st.PluggedIn)
	if st.Phase == calibration.PhaseHold && st.RemainingHoldSecs > 0 {
		fmt.Printf("Hold Remaining: %s\n", bold("%dm%ds", st.RemainingHoldSecs/60, st.RemainingHoldSecs%60))
	}
	if st.TargetPercent > 0 && st.Phase == calibration.PhasePostHold {
		fmt.Printf("Discharge Target: %s\n", bold("%d%%", st.TargetPercent))
	}
	if !st.StartedAt.IsZero() {
		fmt.Printf("Started: %s (%s ago)\n", st.StartedAt.Format(time.RFC3339), time.Since(st.StartedAt).Round(time.Second))
	}
	fmt.Printf("Paused: %v\n", st.Paused)
	fmt.Printf("Can Pause: %v  Can Cancel: %v\n", st.CanPause, st.CanCancel)
	if st.Message != "" {
		fmt.Printf("Message: %s\n", st.Message)
	}
	// Raw JSON (debug flag maybe later). For now always show if error phase.
	if st.Message != "" {
		b, _ := json.MarshalIndent(st, "", "  ")
		fmt.Println("-- raw --")
		fmt.Println(string(b))
	}
}
