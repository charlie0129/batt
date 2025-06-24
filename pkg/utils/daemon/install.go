package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/hack"
)

var (
	plistPath = "/Library/LaunchDaemons/cc.chlc.batt.plist"
)

func Install() error {
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
