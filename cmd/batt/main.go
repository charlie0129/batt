package main

import (
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/charlie0129/batt/pkg/client"
	"github.com/charlie0129/batt/pkg/gui"
	"github.com/charlie0129/batt/pkg/utils/osver"
)

var (
	logLevel       = "info"
	unixSocketPath = "/var/run/batt.sock"
	configPath     = "/etc/batt.json"
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

func setupLogger() error {
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		return fmt.Errorf("failed to parse log level: %v", err)
	}
	logrus.SetLevel(level)
	logrus.SetFormatter(&logrus.TextFormatter{})
	if term.IsTerminal(int(os.Stderr.Fd())) {
		logrus.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: time.Kitchen,
		})
	}

	return nil
}

func handleCmdError(err error) {
	if errors.Is(err, client.ErrDaemonNotRunning) {
		fmt.Fprintln(os.Stderr, "\nError: batt daemon is not running")
		fmt.Fprintln(os.Stderr, "Is the daemon running? Have you installed it?")
	} else if errors.Is(err, client.ErrPermissionDenied) {
		fmt.Fprintln(os.Stderr, "\nError: Permission Denied")
		fmt.Fprintln(os.Stderr, "  - Try running the command again with 'sudo'")
		fmt.Fprintln(os.Stderr, "  - Or reinstall the daemon with the '--allow-non-root-access' flag to grant permissions to your user")
	}
}

func main() {
	if !osver.IsAtLeast(11, 0, 0) {
		fmt.Fprintln(os.Stderr, "batt requires macOS 11.0 or later")
		os.Exit(1)
	}

	// Reduce the number of CPUs used by the batt.
	// batt does not need to use much.
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(2)
	}
	runtime.LockOSThread()

	cmd := NewCommand()
	if err := cmd.Execute(); err != nil {
		handleCmdError(err)
		os.Exit(1)
	}
}

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "batt",
		Short: "batt is a tool to control battery charging on Apple Silicon MacBooks",
		Long: `batt is a tool to control battery charging on Apple Silicon MacBooks.

Website: https://github.com/charlie0129/batt
Report issues: https://github.com/charlie0129/batt/issues`,
		SilenceUsage: true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			err := setupLogger()
			if err != nil {
				return err
			}

			if clientVersion, daemonVersion, err := getVersion(); err == nil {
				if daemonVersion != clientVersion {
					logrus.WithFields(logrus.Fields{
						"clientVersion": clientVersion,
						"daemonVersion": daemonVersion,
					}).Warn("Version mismatch between client and daemon. batt may not work as expected. You should follow the installation / upgrade instructions precisely to ensure both client and daemon are the same version.")
				}
			} else {
				if errors.Is(err, client.ErrNotFound) {
					logrus.Error("batt daemon is too old to report its version. You should follow the installation / upgrade instructions precisely to ensure both client and daemon are the same version.")
				}
			}

			return nil
		},
	}

	if os.Getenv("BATT_RUN_GUI") != "" || path.Base(os.Args[0]) == "batt-gui" {
		cmd.Run = func(_ *cobra.Command, _ []string) {
			gui.Run(unixSocketPath)
		}
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
		NewSetPreventSystemSleepCommand(),
		NewStatusCommand(),
		NewCalibrateCommand(),
		NewAdapterCommand(),
		NewLowerLimitDeltaCommand(),
		NewSetControlMagSafeLEDCommand(),
		NewInstallCommand(),
		NewUninstallCommand(),
		gui.NewGUICommand(unixSocketPath, ""),
	)

	return cmd
}
