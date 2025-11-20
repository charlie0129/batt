package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/calibration"
)

func NewCalibrateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "calibrate",
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

	cmd.AddCommand(startCmd, pauseCmd, resumeCmd, cancelCmd, statusCmd)
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
