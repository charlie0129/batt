package main

import (
	"fmt"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
)

type statusData struct {
	charging      bool
	pluggedIn     bool
	adapter       bool
	currentCharge int
	batteryInfo   *powerinfo.Battery
	config        *config.RawFileConfig
}

// computeTimeToLimit calculates the estimated minutes until the charge limit is
// reached. Returns nil when not applicable (not charging, limit >= 100, charge
// already at/above limit, or result is zero).
func computeTimeToLimit(data *statusData, cfg *config.File) *int {
	if data.batteryInfo.State != powerinfo.Charging || cfg.UpperLimit() >= 100 || data.currentCharge >= cfg.UpperLimit() {
		return nil
	}

	// Work in mAh directly (no Wh conversions)
	designCapacitymAh := float64(data.batteryInfo.Design)
	targetCapacitymAh := float64(cfg.UpperLimit()) / 100.0 * designCapacitymAh
	currentCapacitymAh := float64(data.currentCharge) / 100.0 * designCapacitymAh
	capacityToChargemAh := targetCapacitymAh - currentCapacitymAh

	// Convert charge rate (mW) to mA using V: mA = mW / V
	var chargeRatemA float64
	if data.batteryInfo.DesignVoltage > 0 {
		chargeRatemA = float64(data.batteryInfo.ChargeRate) / data.batteryInfo.DesignVoltage
	}

	if chargeRatemA <= 0 || capacityToChargemAh <= 0 {
		return nil
	}

	timeToLimitHours := capacityToChargemAh / chargeRatemA
	minutes := int(timeToLimitHours * 60)
	if minutes <= 0 {
		return nil
	}

	return &minutes
}

// fetchStatusData gathers all data required for the status command from the daemon.
func fetchStatusData() (*statusData, error) {
	charging, err := apiClient.GetCharging()
	if err != nil {
		return nil, fmt.Errorf("failed to get charging status: %w", err)
	}

	pluggedIn, err := apiClient.GetPluggedIn()
	if err != nil {
		return nil, fmt.Errorf("failed to check if you are plugged in: %w", err)
	}

	adapter, err := apiClient.GetAdapter()
	if err != nil {
		return nil, fmt.Errorf("failed to get power adapter status: %w", err)
	}

	currentCharge, err := apiClient.GetCurrentCharge()
	if err != nil {
		return nil, fmt.Errorf("failed to get current charge: %w", err)
	}

	bat, err := apiClient.GetBatteryInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get battery info: %w", err)
	}

	conf, err := apiClient.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	return &statusData{
		charging:      charging,
		pluggedIn:     pluggedIn,
		adapter:       adapter,
		currentCharge: currentCharge,
		batteryInfo:   bat,
		config:        conf,
	}, nil
}

//nolint:gocyclo
func NewStatusCommand() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
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

			cfg := config.NewFileFromConfig(data.config, "")

			if jsonOutput {
				return printStatusJSON(cmd, data, cfg)
			}

			// Charging status.
			cmd.Println(bold("Charging status:"))

			additionalMsg := " (refreshes can take up to 2 minutes)"
			//nolint:gocritic
			if data.charging {
				cmd.Println("  Allow charging: " + bool2Text(true) + additionalMsg)
				cmd.Print("    Your Mac will charge")
				if !data.pluggedIn {
					cmd.Print(", but you are not plugged in yet.") // not plugged in but charging is allowed.
				} else {
					cmd.Print(".") // plugged in and charging is allowed.
				}
				cmd.Println()
			} else if cfg.UpperLimit() < 100 {
				cmd.Println("  Allow charging: " + bool2Text(false) + additionalMsg)
				cmd.Print("    Your Mac will not charge")
				if data.pluggedIn {
					cmd.Print(" even if you plug in")
				}
				low := cfg.LowerLimit()
				if data.currentCharge >= cfg.UpperLimit() {
					cmd.Print(", because your current charge is above the limit.")
				} else if data.currentCharge < cfg.UpperLimit() && data.currentCharge >= low {
					cmd.Print(", because your current charge is above the lower limit. Charging will be allowed after current charge drops below the lower limit.")
				}
				if data.pluggedIn && data.currentCharge < low {
					if data.adapter {
						cmd.Print(". However, if no manual intervention is involved, charging should be allowed soon. Wait 2 minutes and come back.")
					} else {
						cmd.Print(", because adapter is disabled.")
					}
				}
				cmd.Println()
			} else { // not charging and limit is 100%
				cmd.Println("  Allow charging: " + bool2Text(false) + additionalMsg)
				cmd.Print("    Your Mac will not charge")
			}

			if data.adapter {
				cmd.Println("  Use power adapter: " + bool2Text(true))
				cmd.Println("    Your Mac will use power from the wall (to operate or charge), if it is plugged in.")
			} else {
				cmd.Println("  Use power adapter: " + bool2Text(false))
				cmd.Println("    Your Mac will not use power from the wall (to operate or charge), even if it is plugged in.")
			}

			cmd.Println()

			// Battery Info.
			cmd.Println(bold("Battery status:"))

			cmd.Printf("  Current charge: %s\n", bold("%d%%", data.currentCharge))

			if ttl := computeTimeToLimit(data, cfg); ttl != nil {
				cmd.Printf("  Time to limit (%d%%): %s\n", cfg.UpperLimit(), bold("~%d minutes", *ttl))
			}

			var displayState string
			switch data.batteryInfo.State {
			case powerinfo.Charging:
				displayState = color.GreenString("charging")
			case powerinfo.Discharging:
				if data.batteryInfo.ChargeRate != 0 {
					displayState = color.RedString("discharging")
				} else {
					displayState = "not charging"
				}
			case powerinfo.Full:
				displayState = "full"
			default:
				displayState = "not charging"
			}
			cmd.Printf("  State: %s\n", bold("%s", displayState))
			cmd.Printf("  Full capacity: %s\n", bold("%d mAh", data.batteryInfo.Design))
			// Show charge rate in Watts with sign (+ charging, - discharging) and bright color (bold)
			watts := float64(data.batteryInfo.ChargeRate) / 1e3
			var rateStr string
			switch {
			case watts > 0:
				rateStr = color.New(color.Bold, color.FgGreen).Sprintf("%+.1f W", watts)
			case watts < 0:
				rateStr = color.New(color.Bold, color.FgRed).Sprintf("%+.1f W", watts)
			default:
				rateStr = bold("%+.1f W", watts)
			}
			cmd.Printf("  Charge rate: %s\n", rateStr)
			cmd.Printf("  Voltage: %s\n", bold("%.2f V", data.batteryInfo.DesignVoltage))

			cmd.Println()

			// Config.
			cmd.Println(bold("Battery configuration:"))
			if cfg.UpperLimit() < 100 {
				cmd.Printf("  Upper limit: %s\n", bold("%d%%", cfg.UpperLimit()))
				cmd.Printf("  Lower limit: %s\n", bold("%d%%", cfg.LowerLimit()))
			} else {
				cmd.Printf("  Charge limit: %s\n", bold("100%% (batt disabled)"))
			}
			cmd.Printf("  Prevent idle-sleep when charging: %s\n", bool2Text(cfg.PreventIdleSleep()))
			cmd.Printf("  Disable charging before sleep if charge limit is enabled: %s\n", bool2Text(cfg.DisableChargingPreSleep()))
			cmd.Printf("  Prevent system-sleep when charging: %s\n", bool2Text(cfg.PreventSystemSleep()))
			cmd.Printf("  Allow non-root users to access the daemon: %s\n", bool2Text(cfg.AllowNonRootAccess()))

			mode := cfg.ControlMagSafeLED()
			enabled := mode != config.ControlMagSafeModeDisabled
			ledStatus := bool2Text(enabled)
			if mode == config.ControlMagSafeModeAlwaysOff {
				ledStatus += " (" + bold("always off") + ")"
			}
			cmd.Printf("  Control MagSafe LED: %s\n", ledStatus)

			cmd.Println()

			tr, err := apiClient.GetTelemetry(false, true)
			if err == nil {
				cmd.Println(bold("Calibration status:"))
				cmd.Printf("  Phase: %s\n", bold("%s", string(tr.Calibration.Phase)))
				if tr.Calibration.Phase != calibration.PhaseIdle {
					cmd.Printf("  Start: %s\n", bold("%s", tr.Calibration.StartedAt.Format(time.DateTime)))
				}

				cron := cfg.Cron()
				if cron == "" {
					cmd.Printf("  Schedule: %s\n", bold("disabled"))
				} else {
					cmd.Printf("  Schedule: %s (%s)\n", bold("%s", tr.Calibration.ScheduledAt.Format(time.DateTime)), cfg.Cron())
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output status in JSON format")

	return cmd
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
