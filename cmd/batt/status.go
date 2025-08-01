package main

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/peterneutron/powerkit-go/pkg/powerkit"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/client"
	"github.com/charlie0129/batt/pkg/config"
)

type statusData struct {
	systemInfo *powerkit.SystemInfo
	config     *config.RawFileConfig
}

var apiClient = client.NewClient("/var/run/batt.sock")

// fetchStatusData gathers all data required for the status command from the daemon.
func fetchStatusData() (*statusData, error) {
	sysInfo, err := apiClient.GetSystemInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get system info: %w", err)
	}

	conf, err := apiClient.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	return &statusData{
		systemInfo: sysInfo,
		config:     conf,
	}, nil
}

func NewStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		GroupID: gBasic,
		Short:   "Get the current status of batt",
		Long:    `Get batt status, battery info, and configuration.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Get various info first.
			data, err := fetchStatusData()
			if err != nil {
				return err
			}

			config := config.NewFileFromConfig(data.config, "")
			sysInfo := data.systemInfo

			// Define states from both sources for clarity
			isIOKitConnected := sysInfo.IOKit.State.IsConnected
			isIOKitCharging := sysInfo.IOKit.State.IsCharging
			currentCharge := sysInfo.IOKit.Battery.CurrentCharge
			limit := config.UpperLimit()

			// Charging status.
			cmd.Println(bold("Charging status:"))
			printAllowCharging(cmd, sysInfo, config)

			cmd.Print("  Use power adapter: ")
			if sysInfo.SMC.State.IsAdapterEnabled {
				cmd.Println(bool2Text(true))
				cmd.Println("    Your Mac will use power from the wall when plugged in.")
			} else {
				cmd.Println(bool2Text(false))
				cmd.Println("    Your Mac is set to use the battery, even when plugged in (forced discharge).")
			}

			cmd.Println()

			// Battery Info.
			cmd.Println(bold("Battery status:"))

			cmd.Printf("  Current charge: %s\n", bold("%d%%", currentCharge))

			if isIOKitCharging && limit < 100 && currentCharge < limit {
				if sysInfo.IOKit.Battery.TimeToFull < 65535 {
					cmd.Printf("  Time to limit (%d%%): %s\n", limit, bold("~%d minutes", sysInfo.IOKit.Battery.TimeToFull))
				}
			}

			state := "Not Charging"
			if sysInfo.IOKit.State.FullyCharged {
				state = "Full"
			} else if isIOKitCharging {
				state = color.GreenString("Charging")
			} else if isIOKitConnected {
				state = "Not Charging" // Plugged in but not charging (e.g., at limit)
			} else {
				state = color.RedString("Discharging")
			}
			cmd.Printf("  State: %s\n", bold("%s", state))
			cmd.Printf("  Health: %s\n", bold("%d%%", sysInfo.IOKit.Calculations.HealthByMaxCapacity))
			cmd.Printf("  Cycle Count: %s\n", bold("%d", sysInfo.IOKit.Battery.CycleCount))
			cmd.Printf("  Temperature: %s\n", bold("%.2f °C", sysInfo.IOKit.Battery.Temperature))
			cmd.Printf("  System Power: %s\n", bold("%.2f W", sysInfo.IOKit.Calculations.SystemPower))
			cmd.Printf("  Battery Power: %s\n", bold("%.2f W", sysInfo.IOKit.Calculations.BatteryPower))
			cmd.Printf("  AC Power: %s\n", bold("%.2f W", sysInfo.IOKit.Calculations.AdapterPower))
			cmd.Println()

			// Config.
			cmd.Println(bold("Battery configuration:"))
			if limit < 100 {
				cmd.Printf("  Upper limit: %s\n", bold("%d%%", limit))
				cmd.Printf("  Lower limit: %s\n", bold("%d%%", config.LowerLimit()))
			} else {
				cmd.Printf("  Charge limit: %s\n", bold("100%% (batt disabled)"))
			}
			cmd.Printf("  Prevent idle-sleep when charging: %s\n", bool2Text(config.PreventIdleSleep()))
			cmd.Printf("  Disable charging before sleep if charge limit is enabled: %s\n", bool2Text(config.DisableChargingPreSleep()))
			cmd.Printf("  Allow non-root users to access the daemon: %s\n", bool2Text(config.AllowNonRootAccess()))
			cmd.Printf("  Control MagSafe LED: %s\n", bool2Text(config.ControlMagSafeLED()))
			return nil
		},
	}
}

func printAllowCharging(cmd *cobra.Command, sysInfo *powerkit.SystemInfo, conf config.Config) {
	isPluggedIn := sysInfo.IOKit.State.IsConnected
	isCharging := sysInfo.IOKit.State.IsCharging
	limit := conf.UpperLimit()

	cmd.Print("  Allow charging: ")
	if limit >= 100 {
		cmd.Println(bool2Text(true))
		if isPluggedIn {
			if isCharging {
				cmd.Println("    Your Mac is currently charging.")
			} else {
				cmd.Println("    Your Mac is fully charged.")
			}
		} else {
			cmd.Println("    Your Mac will charge when you plug it in.")
		}
		return
	}

	// Limit is enabled
	if sysInfo.SMC.State.IsChargingEnabled {
		cmd.Println(bool2Text(true))
		if isPluggedIn {
			if isCharging {
				cmd.Printf("    Your Mac is charging up to %d%%.\n", limit)
			} else {
				cmd.Printf("    Your Mac is set to charge up to %d%%, but is not currently charging.\n", limit)
			}
		} else {
			cmd.Printf("    Your Mac will charge up to %d%% when you plug it in.\n", limit)
		}
	} else {
		cmd.Println(bool2Text(false))
		if isPluggedIn {
			cmd.Printf("    Charge limit is %d%%. Your Mac is not charging to preserve battery health.\n", limit)
		} else {
			cmd.Printf("    Charge limit is %d%%. Your Mac will not charge if you plug it in now.\n", limit)
		}
	}
}

func bool2Text(b bool) string {
	if b {
		return color.New(color.Bold, color.FgGreen).Sprint("✔")
	}
	return color.New(color.Bold, color.FgRed).Sprint("✘")
}

func bold(format string, a ...interface{}) string {
	return color.New(color.Bold).Sprintf(format, a...)
}
