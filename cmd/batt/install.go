//go:build !brew

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	pkgerrors "github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/hack"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

func init() {
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

			err = installDaemon()
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

var (
	plistPath = "/Library/LaunchDaemons/cc.chlc.batt.plist"
)

func installDaemon() error {
	// Get the path to the current executable
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get the path to the current executable: %w", err)
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("failed to get the absolute path to the current executable: %w", err)
	}

	err = os.Chmod(exePath, 0755)
	if err != nil {
		return fmt.Errorf("failed to chmod the current executable to 0755: %w", err)
	}

	logrus.Infof("current executable path: %s", exePath)

	tmpl := strings.ReplaceAll(hack.LaunchDaemonPlistTemplate, "/path/to/batt", exePath)

	logrus.Infof("writing launch daemon to /Library/LaunchDaemons")

	// mkdir -p
	err = os.MkdirAll("/Library/LaunchDaemons", 0755)
	if err != nil {
		return fmt.Errorf("failed to create /Library/LaunchDaemons: %w", err)
	}

	// warn if the file already exists
	_, err = os.Stat(plistPath)
	if err == nil {
		logrus.Errorf("%s already exists", plistPath)
	}

	err = os.WriteFile(plistPath, []byte(tmpl), 0644)
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", plistPath, err)
	}

	// chown root:wheel
	err = os.Chown(plistPath, 0, 0)
	if err != nil {
		return fmt.Errorf("failed to chown %s: %w", plistPath, err)
	}

	logrus.Infof("starting batt")

	// run launchctl load /Library/LaunchDaemons/cc.chlc.batt.plist
	err = exec.Command(
		"/bin/launchctl",
		"load",
		plistPath,
	).Run()
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", plistPath, err)
	}

	return nil
}

func uninstallDaemon() error {
	logrus.Infof("stopping batt")

	// run launchctl unload /Library/LaunchDaemons/cc.chlc.batt.plist
	err := exec.Command(
		"/bin/launchctl",
		"unload",
		plistPath,
	).Run()
	if err != nil {
		return fmt.Errorf("failed to unload %s: %w. Are you root?", plistPath, err)
	}

	logrus.Infof("removing launch daemon")

	// if the file doesn't exist, we don't need to remove it
	_, err = os.Stat(plistPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat %s: %w", plistPath, err)
	}

	err = os.Remove(plistPath)
	if err != nil {
		return fmt.Errorf("failed to remove %s: %w. Are you root?", plistPath, err)
	}

	return nil
}
