//go:build !brew

package main

import (
	"fmt"
	"os"

	pkgerrors "github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
	daemonutils "github.com/charlie0129/batt/pkg/utils/daemon"
)

func init() {
	// Only non-homebrew version has install and uninstall commands.
	commandGroups = append(commandGroups, gInstallation)
}

// NewInstallCommand .
func NewInstallCommand() *cobra.Command {
	allowNonRootAccess := false

	cmd := &cobra.Command{
		Use:     "install",
		Short:   "Install batt (system-wide)",
		GroupID: gInstallation,
		Long: `Install batt daemon to launchd (system-wide).

This makes batt run in the background and automatically start on boot. You must run this command as root.

By default, only root user is allowed to access the batt daemon for security reasons. As a result, you will need to run batt client as root to control battery charging, e.g. setting charge limit. If you want to allow non-root users, i.e., you, to access the daemon, you can use the --allow-non-root-access flag, so you don't have to use sudo every time.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conf, err := config.NewFile(configPath)
			if err != nil {
				return err
			}

			conf.SetAllowNonRootAccess(allowNonRootAccess)
			if allowNonRootAccess {
				logrus.Info("non-root users are allowed to access the batt daemon.")
			} else {
				logrus.Info("only root user is allowed to access the batt daemon.")
			}

			err = daemonutils.Install()
			if err != nil {
				// check if current user is root
				if os.Geteuid() != 0 {
					logrus.Errorf("you must run this command as root")
				}
				return fmt.Errorf("failed to install daemon: %v. Are you root?", err)
			}

			err = conf.Save()
			if err != nil {
				return pkgerrors.Wrapf(err, "failed to save config")
			}

			logrus.Infof("installation succeeded")

			exePath, _ := os.Executable()

			cmd.Printf("`launchd' will use current binary (%s) at startup so please make sure you do not move this binary. Once this binary is moved or deleted, you will need to run ``batt install'' again.\n", exePath)

			return nil
		},
	}

	cmd.Flags().BoolVar(&allowNonRootAccess, "allow-non-root-access", false, "Allow non-root users to access batt daemon.")

	return cmd
}

// NewUninstallCommand .
func NewUninstallCommand() *cobra.Command {
	noResetCharging := false

	cmd := &cobra.Command{
		Use:     "uninstall",
		Short:   "Uninstall batt (system-wide)",
		GroupID: gInstallation,
		Long: `Uninstall batt daemon from launchd (system-wide).

This stops batt and removes it from launchd.

You must run this command as root.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := daemonutils.Uninstall()
			if err != nil {
				// check if current user is root
				if os.Geteuid() != 0 {
					logrus.Errorf("you must run this command as root")
				}
				return fmt.Errorf("failed to uninstall daemon: %v", err)
			}

			if !noResetCharging {
				// Open Apple SMC for read/writing
				smcC := smc.New()
				if err := smcC.Open(); err != nil {
					return fmt.Errorf("failed to open SMC: %v", err)
				}

				err = smcC.EnableCharging()
				if err != nil {
					return fmt.Errorf("failed to enable charging: %v", err)
				}

				err = smcC.EnableAdapter()
				if err != nil {
					return fmt.Errorf("failed to enable adapter: %v", err)
				}

				if err := smcC.Close(); err != nil {
					return fmt.Errorf("failed to close SMC: %v", err)
				}
			}

			logrus.Infof("resetting charge limits")

			fmt.Println("successfully uninstalled")

			cmd.Printf("Your config is kept in %s, in case you want to use `batt' again. If you want a complete uninstall, you can remove both config file and batt itself manually.\n", configPath)

			return nil
		},
	}

	cmd.Flags().BoolVar(&noResetCharging, "no-reset-charging", false, "Do not reset charging limits after uninstalling. This is useful if you want to keep the current charging limits for future use.")

	return cmd
}
