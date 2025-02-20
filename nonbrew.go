//go:build !brew

package main

import (
	"fmt"
	"os"

	"github.com/charlie0129/batt/pkg/smc"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func init() {
	commandGroups = append(commandGroups, gInstallation)
}

// NewInstallCommand .
func NewInstallCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "install",
		Short:   "Install batt (system-wide)",
		GroupID: gInstallation,
		Long: `Install batt daemon to launchd (system-wide).
This makes batt run in the background and automatically start on boot. You must run this command as root.

By default, only root user is allowed to access the batt daemon for security reasons. As a result, you will need to run batt as root to control battery charging, e.g. setting charge limit. If you want to allow non-root users, i.e., you, to access the daemon, you can use the --allow-non-root-access flag, so you don't have to use sudo every time. However, bear in mind that it introduces security risks.`,
		PreRunE: func(cmd *cobra.Command, _ []string) error {

			err := loadConfig()
			if err != nil {
				return err
			}

			flags := cmd.Flags()
			b, err := flags.GetBool("allow-non-root-access")
			if err != nil {
				return err
			}

			if config.AllowNonRootAccess && !b {
				logrus.Warnf("Previously, non-root users were allowed to access the batt daemon. However, this will be disabled at every installation unless you provide the --allow-non-root-access flag.")
			}

			// Before installation, always reset config.AllowNonRootAccess to flag value
			// instead of the one in config file.
			config.AllowNonRootAccess = b

			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if config.AllowNonRootAccess {
				logrus.Warnf("non-root users are allowed to access the batt daemon. It can be a security risk.")
			}

			err := installDaemon()
			if err != nil {
				// check if current user is root
				if os.Geteuid() != 0 {
					logrus.Errorf("you must run this command as root")
				}
				return fmt.Errorf("failed to install daemon: %v. Are you root?", err)
			}

			err = saveConfig()
			if err != nil {
				return err
			}

			logrus.Infof("installation succeeded")

			exePath, _ := os.Executable()

			cmd.Printf("`launchd' will use current binary (%s) at startup so please make sure you do not move this binary. Once this binary is moved or deleted, you will need to run ``batt install'' again.\n", exePath)

			return nil
		},
	}

	cmd.Flags().Bool("allow-non-root-access", false, "Allow non-root users to access batt daemon.")

	return cmd
}

// NewUninstallCommand .
func NewUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "uninstall",
		Short:   "Uninstall batt (system-wide)",
		GroupID: gInstallation,
		Long: `Uninstall batt daemon from launchd (system-wide).
This stops batt and removes it from launchd.

You must run this command as root.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := uninstallDaemon()
			if err != nil {
				// check if current user is root
				if os.Geteuid() != 0 {
					logrus.Errorf("you must run this command as root")
				}
				return fmt.Errorf("failed to uninstall daemon: %v", err)
			}

			logrus.Infof("resetting charge limits")

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

			fmt.Println("successfully uninstalled")

			cmd.Printf("Your config is kept in %s, in case you want to use `batt' again. If you want a complete uninstall, you can remove both config file and batt itself manually.\n", configPath)

			return nil
		},
	}
}
