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
	plistPath     = "/Library/LaunchDaemons/cc.chlc.batt.plist"
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

	// warn if the file already exists
	_, err = os.Stat(plistPath)
	if err == nil {
		logrus.Errorf("%s already exists", plistPath)
		return fmt.Errorf("%s already exists. This is often caused by an incorrect installation. Did you forget to uninstall batt before installing it again? Please uninstall it first, by running 'sudo batt uninstall'. If you already removed batt, you can solve this problem by 'sudo rm %s'", plistPath, plistPath)
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
