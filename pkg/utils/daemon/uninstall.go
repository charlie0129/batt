package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
)

func Uninstall() error {
	slog.Info("stopping batt")

	// run launchctl unload /Library/LaunchDaemons/cc.chlc.batt.plist
	err := exec.Command(
		"/bin/launchctl",
		"unload",
		plistPath,
	).Run()
	if err != nil {
		return fmt.Errorf("failed to unload %s: %w. Are you root?", plistPath, err)
	}

	slog.Info("removing launch daemon")

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
