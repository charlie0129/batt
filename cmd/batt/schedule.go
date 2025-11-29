package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func NewScheduleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "schedule [cron expression | disable | postpone <duration> | skip]",
		Aliases: []string{"sch"},
		Short:   "Manage automatic calibration schedule",
		Long: `Examples:
  batt schedule '0 10 * * 0' # Note: Cron expressions containing * must be quoted!
  batt schedule disable
  batt schedule postpone 90m
  batt schedule skip`,
		GroupID: gAdvanced,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Usage()
			}
			switch strings.ToLower(args[0]) {
			case "disable":
				return runScheduleDisable(cmd)
			case "postpone":
				return runSchedulePostpone(cmd, args[1:])
			case "skip":
				return runScheduleSkip(cmd)
			default:
				cronExpr := strings.Join(args, " ")
				return runScheduleSet(cmd, cronExpr)
			}
		},
	}
	return cmd
}

func runScheduleSet(cmd *cobra.Command, cronExpr string) error {
	if strings.TrimSpace(cronExpr) == "" {
		return fmt.Errorf("cron expression cannot be empty")
	}
	nextRuns, err := apiClient.Schedule(cronExpr)
	if err != nil {
		return err
	}
	if len(nextRuns) == 0 {
		cmd.Println("Calibration schedule disabled.")
		return nil
	}
	cmd.Printf("Calibration scheduled. Next %d run(s):\n", len(nextRuns))
	for _, run := range nextRuns {
		cmd.Printf("  - %s\n", run.Local().Format(time.DateTime))
	}
	return nil
}

func runScheduleDisable(cmd *cobra.Command) error {
	if _, err := apiClient.Schedule(""); err != nil {
		return err
	}
	cmd.Println("Calibration schedule disabled.")
	return nil
}

func runSchedulePostpone(cmd *cobra.Command, args []string) error {
	duration := time.Hour
	if len(args) > 0 {
		parsed, err := time.ParseDuration(args[0])
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", args[0], err)
		}
		duration = parsed
	}
	if _, err := apiClient.PostponeSchedule(duration); err != nil {
		return err
	}
	cmd.Printf("Next run postponed by %s.\n", duration)
	return nil
}

func runScheduleSkip(cmd *cobra.Command) error {
	if _, err := apiClient.SkipSchedule(); err != nil {
		return err
	}
	cmd.Println("Next scheduled run skipped.")
	return nil
}
