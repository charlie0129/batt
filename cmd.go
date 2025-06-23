package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/distatus/battery"
	"github.com/fatih/color"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/internal/client"
	"github.com/charlie0129/batt/pkg/version"
)

var (
	logLevel = "info"
)

var (
	gBasic        = "Basic:"
	gAdvanced     = "Advanced:"
	gInstallation = "Installation:"
	commandGroups = []string{
		gBasic,
		gAdvanced,
	}
)

type statusData struct {
	charging      bool
	pluggedIn     bool
	adapter       bool
	currentCharge int
	batteryInfo   *battery.Battery
	config        *Config
}

var apiClient = client.NewClient("/var/run/batt.sock")

// fetchStatusData gathers all data required for the status command from the daemon.
func fetchStatusData() (*statusData, error) {
	ret, err := apiClient.Get("/charging")
	if err != nil {
		return nil, fmt.Errorf("failed to get charging status: %w", err)
	}
	charging, err := strconv.ParseBool(ret)
	if err != nil {
		return nil, err
	}

	ret, err = apiClient.Get("/plugged-in")
	if err != nil {
		return nil, fmt.Errorf("failed to check if you are plugged in: %w", err)
	}
	pluggedIn, err := strconv.ParseBool(ret)
	if err != nil {
		return nil, err
	}

	ret, err = apiClient.Get("/adapter")
	if err != nil {
		return nil, fmt.Errorf("failed to get power adapter status: %w", err)
	}
	adapter, err := strconv.ParseBool(ret)
	if err != nil {
		return nil, err
	}

	ret, err = apiClient.Get("/current-charge")
	if err != nil {
		return nil, fmt.Errorf("failed to get current charge: %w", err)
	}
	currentCharge, err := strconv.Atoi(ret)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal current charge: %w", err)
	}

	ret, err = apiClient.Get("/battery-info")
	if err != nil {
		return nil, fmt.Errorf("failed to get battery info: %w", err)
	}
	var bat battery.Battery
	if err := json.Unmarshal([]byte(ret), &bat); err != nil {
		return nil, fmt.Errorf("failed to unmarshal battery info: %w", err)
	}

	ret, err = apiClient.Get("/config")
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	var conf Config
	if err := json.Unmarshal([]byte(ret), &conf); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &statusData{
		charging:      charging,
		pluggedIn:     pluggedIn,
		adapter:       adapter,
		currentCharge: currentCharge,
		batteryInfo:   &bat,
		config:        &conf,
	}, nil
}

// NewCommand .
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "batt",
		Short: "batt is a tool to control battery charging on Apple Silicon MacBooks",
		Long: `batt is a tool to control battery charging on Apple Silicon MacBooks.

Website: https://github.com/charlie0129/batt`,
		SilenceUsage: true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			return setupLogger()
		},
	}

	globalFlags := cmd.PersistentFlags()
	globalFlags.StringVarP(&logLevel, "log-level", "l", "info", "log level (trace, debug, info, warn, error, fatal, panic)")
	globalFlags.StringVar(&configPath, "config", configPath, "config file path")
	globalFlags.StringVar(&unixSocketPath, "daemon-socket", unixSocketPath, "batt daemon unix socket path")

	for _, i := range commandGroups {
		cmd.AddGroup(&cobra.Group{
			ID:    i,
			Title: i,
		})
	}

	cmd.AddCommand(
		NewDaemonCommand(),
		NewVersionCommand(),
		NewLimitCommand(),
		NewDisableCommand(),
		NewSetDisableChargingPreSleepCommand(),
		NewSetPreventIdleSleepCommand(),
		NewStatusCommand(),
		NewAdapterCommand(),
		NewLowerLimitDeltaCommand(),
		NewSetControlMagSafeLEDCommand(),
		NewInstallCommand(),
		NewUninstallCommand(),
	)

	return cmd
}

// NewVersionCommand .
func NewVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Printf("%s %s\n", version.Version, version.GitCommit)
		},
	}
}

// NewDaemonCommand .
func NewDaemonCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "daemon",
		Hidden:  true,
		Short:   "Run batt daemon in the foreground",
		GroupID: gAdvanced,
		Run: func(_ *cobra.Command, _ []string) {
			logrus.Infof("batt version %s commit %s", version.Version, version.GitCommit)
			runDaemon()
		},
	}

	f := cmd.Flags()

	f.BoolVar(&alwaysAllowNonRootAccess, "always-allow-non-root-access", false,
		"Always allow non-root users to access the daemon.")

	return cmd
}

// NewLimitCommand .
func NewLimitCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "limit [percentage]",
		Short:   "Set upper charge limit",
		GroupID: gBasic,
		Long: `Set upper charge limit.

This is a percentage from 10 to 100.

Setting the limit to 10-99 will enable the battery charge limit. However, setting the limit to 100 will disable the battery charge limit, which is the default behavior of macOS.`,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid number of arguments")
			}

			ret, err := apiClient.Put("/limit", args[0])
			if err != nil {
				return fmt.Errorf("failed to set limit: %v", err)
			}

			if ret != "" {
				logrus.Infof("daemon responded: %s", ret)
			}

			logrus.Infof("successfully set battery charge limit to %s%%", args[0])

			return nil
		},
	}
}

// NewDisableCommand .
func NewDisableCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "disable",
		Short:   "Disable batt",
		GroupID: gBasic,
		Long: `Disable batt.

Stop batt from controlling battery charging. This will allow your Mac to charge to 100%.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			ret, err := apiClient.Put("/limit", "100")
			if err != nil {
				return fmt.Errorf("failed to disable batt: %v", err)
			}

			if ret != "" {
				logrus.Infof("daemon responded: %s", ret)
			}

			logrus.Infof("successfully disabled batt. Charge limit has been reset to 100%%. To re-enable batt, just set a charge limit using \"batt limit\".")

			return nil
		},
	}
}

// NewSetPreventIdleSleepCommand .
func NewSetPreventIdleSleepCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "prevent-idle-sleep",
		Short:   "Set whether to prevent idle sleep during a charging session",
		GroupID: gAdvanced,
		Long: `Set whether to prevent idle sleep during a charging session.

Due to macOS limitations, batt will be paused when your computer goes to sleep. As a result, when you are in a charging session and your computer goes to sleep, there is no way for batt to stop charging (since batt is paused by macOS) and the battery will charge to 100%. This option, together with disable-charging-pre-sleep, will prevent this from happening.

This option tells macOS NOT to go to sleep when the computer is in a charging session, so batt can continue to work until charging is finished. Note that it will only prevent **idle** sleep, when 1) charging is active 2) battery charge limit is enabled. So your computer can go to sleep as soon as a charging session is completed.

However, this options does not prevent manual sleep (limitation of macOS). For example, if you manually put your computer to sleep (by choosing the Sleep option in the top-left Apple menu) or close the lid, batt will still be paused and the issue mentioned above will still happen. This is where disable-charging-pre-sleep comes in.`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Prevent idle sleep during a charging session",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.Put("/prevent-idle-sleep", "true")
				if err != nil {
					return fmt.Errorf("failed to set prevent idle sleep: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully enabled idle sleep prevention")

				return nil
			},
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Do not prevent idle sleep during a charging session",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.Put("/prevent-idle-sleep", "false")
				if err != nil {
					return fmt.Errorf("failed to set prevent idle sleep: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully disabled idle sleep prevention")

				return nil
			},
		},
	)

	return cmd
}

// NewSetDisableChargingPreSleepCommand .
func NewSetDisableChargingPreSleepCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "disable-charging-pre-sleep",
		Short:   "Set whether to disable charging before sleep if charge limit is enabled",
		GroupID: gAdvanced,
		Long: `Set whether to disable charging before sleep if charge limit is enabled.

As described in preventing-idle-sleep, batt will be paused by macOS when your computer goes to sleep, and there is no way for batt to continue controlling battery charging. This option will disable charging just before sleep, so your computer will not overcharge during sleep, even if the battery charge is below the limit.`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Disable charging before sleep during a charging session",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.Put("/disable-charging-pre-sleep", "true")
				if err != nil {
					return fmt.Errorf("failed to set disable charging pre sleep: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully enabled disable-charging-pre-sleep")

				return nil
			},
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Do not disable charging before sleep during a charging session",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.Put("/disable-charging-pre-sleep", "false")
				if err != nil {
					return fmt.Errorf("failed to set disable charging pre sleep: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully disabled disable-charging-pre-sleep")

				return nil
			},
		},
	)

	return cmd
}

// NewAdapterCommand .
func NewAdapterCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "adapter",
		Short:   "Enable or disable power input",
		GroupID: gBasic,
		Long: `Cut or restore power from the wall. This has the same effect as unplugging/plugging the power adapter, even if the adapter is physically plugged in.

This is useful when you want to use your battery to lower the battery charge, but you don't want to unplug the power adapter.

NOTE: if you are using Clamshell mode (using a Mac laptop with an external monitor and the lid closed), *cutting power will cause your Mac to go to sleep*. This is a limitation of macOS. There are ways to prevent this, but it is not recommended for most users.`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "disable",
			Short: "Disable power adapter",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.Put("/adapter", "false")
				if err != nil {
					return fmt.Errorf("failed to disable power adapter: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully disabled power adapter")

				return nil
			},
		},
		&cobra.Command{
			Use:   "enable",
			Short: "Enable power adapter",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.Put("/adapter", "true")
				if err != nil {
					return fmt.Errorf("failed to enable power adapter: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully enabled power adapter")

				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Get the current status of power adapter",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.Get("/adapter")
				if err != nil {
					return fmt.Errorf("failed to get power adapter status: %v", err)
				}

				switch ret {
				case "true":
					logrus.Infof("power adapter is enabled")
				case "false":
					logrus.Infof("power adapter is disabled")
				default:
					logrus.Errorf("unknown power adapter status")
				}

				return nil
			},
		},
	)

	return cmd
}

// NewStatusCommand .
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

			// Charging status.
			cmd.Println(bold("Charging status:"))

			additionalMsg := " (refreshes can take up to 2 minutes)"
			if data.charging {
				cmd.Println("  Allow charging: " + bool2Text(true) + additionalMsg)
				cmd.Print("    Your Mac will charge")
				if !data.pluggedIn {
					cmd.Print(", but you are not plugged in yet.") // not plugged in but charging is allowed.
				} else {
					cmd.Print(".") // plugged in and charging is allowed.
				}
				cmd.Println()
			} else if data.config.Limit < 100 {
				cmd.Println("  Allow charging: " + bool2Text(false) + additionalMsg)
				cmd.Print("    Your Mac will not charge")
				if data.pluggedIn {
					cmd.Print(" even if you plug in")
				}
				low := config.Limit - config.LowerLimitDelta
				if data.currentCharge >= config.Limit {
					cmd.Print(", because your current charge is above the limit.")
				} else if data.currentCharge < config.Limit && data.currentCharge >= low {
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

			if data.batteryInfo.State == battery.Charging && data.config.Limit < 100 && data.currentCharge < data.config.Limit {
				designCapacityWh := data.batteryInfo.Design / 1000.0
				chargeRateW := data.batteryInfo.ChargeRate / 1000.0

				targetCapacityWh := float64(data.config.Limit) / 100.0 * designCapacityWh
				currentCapacityWh := float64(data.currentCharge) / 100.0 * designCapacityWh
				capacityToChargeWh := targetCapacityWh - currentCapacityWh

				if chargeRateW > 0 && capacityToChargeWh > 0 {
					timeToLimitHours := capacityToChargeWh / chargeRateW
					timeToLimitMinutes := float64(timeToLimitHours * 60)

					if timeToLimitMinutes > 0.00 {
						cmd.Printf("  Time to limit (%d%%): %s\n", data.config.Limit, bold("~%d minutes", int(timeToLimitMinutes)))
					}
				}
			}

			state := "not charging"
			switch data.batteryInfo.State {
			case battery.Charging:
				state = color.GreenString("charging")
			case battery.Discharging:
				state = color.RedString("discharging")
			case battery.Full:
				state = "full"
			}
			cmd.Printf("  State: %s\n", bold("%s", state))
			cmd.Printf("  Full capacity: %s\n", bold("%.1f Wh", data.batteryInfo.Design/1e3))
			cmd.Printf("  Charge rate: %s\n", bold("%.1f W", data.batteryInfo.ChargeRate/1e3))
			cmd.Printf("  Voltage: %s\n", bold("%.2f V", data.batteryInfo.DesignVoltage))

			cmd.Println()

			// Config.
			cmd.Println(bold("Battery configuration:"))
			if data.config.Limit < 100 {
				cmd.Printf("  Upper limit: %s\n", bold("%d%%", data.config.Limit))
				cmd.Printf("  Lower limit: %s (%d-%d)\n", bold("%d%%", data.config.Limit-data.config.LowerLimitDelta), data.config.Limit, data.config.LowerLimitDelta)
			} else {
				cmd.Printf("  Charge limit: %s\n", bold("100%% (batt disabled)"))
			}
			cmd.Printf("  Prevent idle-sleep when charging: %s\n", bool2Text(data.config.PreventIdleSleep))
			cmd.Printf("  Disable charging before sleep if charge limit is enabled: %s\n", bool2Text(data.config.DisableChargingPreSleep))
			cmd.Printf("  Allow non-root users to access the daemon: %s\n", bool2Text(data.config.AllowNonRootAccess))
			cmd.Printf("  Control MagSafe LED: %s\n", bool2Text(data.config.ControlMagSafeLED))
			return nil
		},
	}
}

// NewLowerLimitDeltaCommand .
func NewLowerLimitDeltaCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "lower-limit-delta",
		Short:   "Set the delta between lower and upper charge limit",
		GroupID: gAdvanced,
		Long: `Set the delta between lower and upper charge limit.

When you set a charge limit, for example, on a Lenovo ThinkPad, you can set two percentages. The first one is the upper limit, and the second one is the lower limit. When the battery charge is above the upper limit, the computer will stop charging. When the battery charge is below the lower limit, the computer will start charging. If the battery charge is between the two limits, the computer will keep whatever charging state it is in.

batt have similar features. The charge limit you have set (using 'batt limit') will be used as the upper limit. By default, The lower limit will be set to 2% less than the upper limit. Same as using 'batt lower-limit-delta 2'. To customize the lower limit, use 'batt lower-limit-delta'.

For example, if you want to set the lower limit to be 5% less than the upper limit, run 'sudo batt lower-limit-delta 5'. By doing this, if you have your charge (upper) limit set to 60%, the lower limit will be 55%.`,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid number of arguments")
			}

			delta, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid delta: %v", err)
			}

			ret, err := apiClient.Put("/lower-limit-delta", strconv.Itoa(delta))
			if err != nil {
				return fmt.Errorf("failed to set lower limit delta: %v", err)
			}

			if ret != "" {
				logrus.Infof("daemon responded: %s", ret)
			}

			logrus.Infof("successfully set lower limit delta to %d%%", delta)

			return nil
		},
	}

	return cmd
}

// NewSetControlMagSafeLEDCommand .
func NewSetControlMagSafeLEDCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "magsafe-led",
		Short:   "Control MagSafe LED according to battery charging status",
		GroupID: gAdvanced,
		Long: `This option can make the MagSafe LED on your MacBook change color according to the charging status. For example:

- Green: charge limit is reached and charging is stopped.
- Orange: charging is in progress.
- Off: just woken up from sleep, charing is disabled and batt is waiting before controlling charging.

Note that you must have a MagSafe LED on your MacBook to use this feature.`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Control MagSafe LED according to battery charging status",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.Put("/magsafe-led", "true")
				if err != nil {
					return fmt.Errorf("failed to set magsafe: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully enabled magsafe led controlling")

				return nil
			},
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Do not control MagSafe LED",
			RunE: func(_ *cobra.Command, _ []string) error {
				ret, err := apiClient.Put("/magsafe-led", "false")
				if err != nil {
					return fmt.Errorf("failed to set magsafe: %v", err)
				}

				if ret != "" {
					logrus.Infof("daemon responded: %s", ret)
				}

				logrus.Infof("successfully disabled magsafe led controlling")

				return nil
			},
		},
	)

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
