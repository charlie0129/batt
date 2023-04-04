package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

var (
	//go:embed hack/cc.chlc.batt.plist
	plistTemplate string
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

	tmpl := strings.ReplaceAll(plistTemplate, "/path/to/batt", exePath)

	logrus.Infof("writing launch daemon to /Library/LaunchDaemons")

	// mkdir -p
	err = os.MkdirAll("/Library/LaunchDaemons", 0755)
	if err != nil {
		return fmt.Errorf("failed to create /Library/LaunchDaemons: %w", err)
	}

	err = os.WriteFile("/Library/LaunchDaemons/cc.chlc.batt.plist", []byte(tmpl), 0644)
	if err != nil {
		return fmt.Errorf("failed to write /Library/LaunchDaemons/cc.chlc.batt.plist: %w", err)
	}

	// chown root:wheel
	err = os.Chown("/Library/LaunchDaemons/cc.chlc.batt.plist", 0, 0)
	if err != nil {
		return fmt.Errorf("failed to chown /Library/LaunchDaemons/cc.chlc.batt.plist: %w", err)
	}

	logrus.Infof("starting batt")

	// run launchctl load /Library/LaunchDaemons/cc.chlc.batt.plist
	err = exec.Command(
		"/bin/launchctl",
		"load",
		"/Library/LaunchDaemons/cc.chlc.batt.plist",
	).Run()
	if err != nil {
		return fmt.Errorf("failed to load /Library/LaunchDaemons/cc.chlc.batt.plist: %w", err)
	}

	return nil
}

func uninstallDaemon() error {
	// TODO: revert any changes, enable battery charging and enable adapter before uninstalling

	logrus.Infof("stopping batt")

	// run launchctl unload /Library/LaunchDaemons/cc.chlc.batt.plist
	err := exec.Command(
		"/bin/launchctl",
		"unload",
		"/Library/LaunchDaemons/cc.chlc.batt.plist",
	).Run()
	if err != nil {
		return fmt.Errorf("failed to unload /Library/LaunchDaemons/cc.chlc.batt.plist: %w", err)
	}

	logrus.Infof("removing launch daemon")

	err = os.Remove("/Library/LaunchDaemons/cc.chlc.batt.plist")
	if err != nil {
		return fmt.Errorf("failed to remove /Library/LaunchDaemons/cc.chlc.batt.plist: %w", err)
	}

	return nil
}
