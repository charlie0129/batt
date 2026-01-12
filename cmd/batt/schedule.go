package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func NewScheduleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "schedule [cron-expression]",
		Aliases: []string{"sch", "sche", "sched"},
		Short:   "Manage automatic calibration schedule",
		Long: `Manage automatic calibration schedule.

The schedule command can be used in multiple ways:
  batt schedule 'minute hour day month weekday' Set schedule with cron expression
  batt schedule disable                         Disable the schedule
  batt schedule postpone [duration]             Postpone next run
  batt schedule skip                            Skip next run
  batt schedule show                            Show current schedule`,
		Example: `  batt schedule '0 10 * * 0' (At 10:00 on Sunday)
  batt schedule '0 10 1 * *' (At 10:00 on the first day of every month)
  batt schedule '0 10 1 */2 *' (At 10:00 on the first day of every two months)
  batt schedule '0 10 1 */3 *' (At 10:00 on the first day of every three months)`,
		GroupID: gAdvanced,
		RunE: func(cmd *cobra.Command, args []string) error {
			// If no arguments, show the current schedule
			if len(args) == 0 {
				return runScheduleShow(cmd)
			}
			// Otherwise, treat as a cron expression to set
			return runScheduleSet(cmd, args[0])
		},
	}

	// Add subcommands
	cmd.AddCommand(
		newScheduleDisableCommand(),
		newSchedulePostponeCommand(),
		newScheduleSkipCommand(),
		newScheduleShowCommand(),
	)

	return cmd
}

func newScheduleDisableCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable the calibration schedule",
		Long:  "Disable the automatic calibration schedule.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScheduleDisable(cmd)
		},
	}
	return cmd
}

func newSchedulePostponeCommand() *cobra.Command {
	var duration time.Duration

	cmd := &cobra.Command{
		Use:   "postpone [duration]",
		Short: "Postpone the next scheduled calibration run",
		Example: `  batt schedule postpone      (Postpone by 1 hour)
  batt schedule postpone 90m  (Postpone by 90 minutes)
  batt schedule postpone 2h   (Postpone by 2 hours)`,
		Long: `Postpone the next scheduled calibration run by a specified duration.
If no duration is provided, defaults to 1 hour.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d := time.Hour // default
			if duration != 0 {
				d = duration
			}
			if len(args) > 0 {
				parsed, err := time.ParseDuration(args[0])
				if err != nil {
					return fmt.Errorf("invalid duration %q: %w", args[0], err)
				}
				d = parsed
			}
			return runSchedulePostpone(cmd, d)
		},
	}

	cmd.Flags().DurationVar(&duration, "duration", time.Hour, "Duration to postpone (e.g., 1h, 90m)")
	return cmd
}

func newScheduleSkipCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skip",
		Short: "Skip the next scheduled calibration run",
		Long:  "Skip the next scheduled calibration run.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScheduleSkip(cmd)
		},
	}
	return cmd
}

func newScheduleShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the current calibration schedule",
		Long:  "Show the current calibration schedule and next run times.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScheduleShow(cmd)
		},
	}
	return cmd
}

func runScheduleSet(cmd *cobra.Command, cronExpr string) error {
	if cronExpr == "" {
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

func runSchedulePostpone(cmd *cobra.Command, duration time.Duration) error {
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

func runScheduleShow(cmd *cobra.Command) error {
	nextRuns, err := apiClient.Schedule("")
	if err != nil {
		return err
	}
	if len(nextRuns) == 0 {
		cmd.Println("Calibration schedule is not set.")
		return nil
	}
	cmd.Printf("Next %d run(s):\n", len(nextRuns))
	for _, run := range nextRuns {
		cmd.Printf("  - %s\n", run.Local().Format(time.DateTime))
	}
	return nil
}
