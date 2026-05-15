package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/temperature"
)

func NewTemperatureCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "temperature",
		Aliases: []string{"temp"},
		GroupID: gAdvanced,
		Short:   "Manage temperature monitoring and protection",
		Long: `Manage temperature monitoring and protection.

When temperature monitoring is enabled, batt records battery temperature references for:
1) idle and not charging
2) idle and charging
3) active and charging

The references are written as auto-generated comments in the config file. Temperature protection disables charging when the battery reaches the configured threshold and releases protection after it cools down.`,
	}

	monitoringCmd := &cobra.Command{
		Use:   "monitoring",
		Short: "Enable or disable temperature reference recording",
		Long:  "Enable or disable temperature reference recording. Recorded references are written as auto-generated comments in the config file.",
	}

	monitoringCmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Enable temperature reference recording",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.SetTemperatureMonitoring(true)
				if err != nil {
					return fmt.Errorf("failed to enable temperature monitoring: %v", err)
				}
				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}
				logrus.Infof("successfully enabled temperature monitoring")
				return nil
			},
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Disable temperature reference recording and protection",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.SetTemperatureMonitoring(false)
				if err != nil {
					return fmt.Errorf("failed to disable temperature monitoring: %v", err)
				}
				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}
				logrus.Infof("successfully disabled temperature monitoring")
				return nil
			},
		},
	)

	thresholdCmd := &cobra.Command{
		Use:   "threshold [celsius]",
		Short: "Set temperature protection threshold",
		Long:  "Set the battery temperature protection threshold in Celsius. Valid range: 30-55.",
		RunE: func(_ *cobra.Command, args []string) error {
			threshold, err := parseIntArg(args, "temperature threshold")
			if err != nil {
				return err
			}
			if threshold < 30 || threshold > 55 {
				return fmt.Errorf("temperature protection threshold must be between 30°C and 55°C, got %d°C", threshold)
			}

			ret, err := apiClient.SetTemperatureProtectionThresholdCelsius(threshold)
			if err != nil {
				return fmt.Errorf("failed to set temperature protection threshold: %v", err)
			}
			if ret != "" {
				logrus.Infof("daemon responded: %s", ret)
			}
			logrus.Infof("successfully set temperature protection threshold to %d°C", threshold)
			return nil
		},
	}

	var jsonOutput bool
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Get current temperature monitoring status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := apiClient.GetTemperatureStatus()
			if err != nil {
				return fmt.Errorf("failed to get temperature status: %v", err)
			}
			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(status)
			}

			printTemperatureStatus(cmd, status)
			return nil
		},
	}
	statusCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output status in JSON format")

	cmd.AddCommand(monitoringCmd, thresholdCmd, statusCmd)
	return cmd
}

func printTemperatureStatus(cmd *cobra.Command, status *temperature.Status) {
	cmd.Println(bold("Temperature status:"))
	cmd.Printf("  Monitoring: %s\n", bool2Text(status.MonitoringEnabled))
	cmd.Printf("  Protection: %s\n", protectionStateText(status.ProtectionActive))
	cmd.Printf("  Threshold: %s\n", bold("%d°C", status.ProtectionThresholdCelsius))
	cmd.Printf("  Recovery threshold: %s\n", bold("%d°C", status.RecoveryThresholdCelsius))

	if status.CurrentCelsius == nil {
		if status.TemperatureUnavailableReason != "" {
			cmd.Printf("  Current temperature: %s (%s)\n", bold("unavailable"), status.TemperatureUnavailableReason)
		} else {
			cmd.Printf("  Current temperature: %s\n", bold("no data yet"))
		}
	} else {
		cmd.Printf("  Current temperature: %s\n", bold("%.1f°C", *status.CurrentCelsius))
	}

	if status.LastUpdatedUnix > 0 {
		cmd.Printf("  Last sample: %s\n", bold("%s", time.Unix(status.LastUpdatedUnix, 0).Format(time.DateTime)))
	}

	hasSample := status.LastUpdatedUnix > 0 || status.CurrentCelsius != nil
	if status.ActivityUnavailableReason != "" {
		cmd.Printf("  Activity detection: %s (%s)\n", bold("unavailable"), status.ActivityUnavailableReason)
	} else if !hasSample {
		cmd.Printf("  Activity: %s\n", bold("no data yet"))
	} else {
		cmd.Printf("  Activity: %s\n", activityStateText(status.UserActive))
	}
	if hasSample {
		cmd.Printf("  Sample charging state: %s\n", chargingStateText(status.Charging))
	} else {
		cmd.Printf("  Sample charging state: %s\n", bold("no data yet"))
	}

	cmd.Println()
	cmd.Println(bold("Temperature references:"))
	cmd.Printf("  %s\n", temperatureReferenceText("Idle + Not Charging", status.References.IdleNotCharging))
	cmd.Printf("  %s\n", temperatureReferenceText("Idle + Charging", status.References.IdleCharging))
	cmd.Printf("  %s\n", temperatureReferenceText("Active + Charging", status.References.ActiveCharging))
}

func protectionStateText(active bool) string {
	if active {
		return bold("active")
	}
	return "inactive"
}

func activityStateText(active bool) string {
	if active {
		return "active"
	}
	return "idle"
}

func chargingStateText(charging bool) string {
	if charging {
		return "charging"
	}
	return "not charging"
}

func temperatureReferenceText(label string, value *float64) string {
	if value == nil {
		return fmt.Sprintf("%s: %s", label, bold("no data yet"))
	}
	return fmt.Sprintf("%s: %s", label, bold("%.1f°C", *value))
}
