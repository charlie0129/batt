package gui

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	pkgerrors "github.com/pkg/errors"
)

var (
	battSymlinkLocation = "/usr/local/bin/batt"
)

func isDaemonInstalled() bool {
	plistPath := "/Library/LaunchDaemons/cc.chlc.batt.plist"
	_, err := os.Stat(plistPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		slog.Warn(fmt.Sprintf("Failed to check if %s exists: %s", plistPath, err))
		return false
	}
	return true
}

func escapeShellInAppleScript(in string) string {
	out := strings.Builder{}
	for _, r := range in {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// uninstallDaemon removes daemon and resets charging limits.
func uninstallDaemon(exe string) error {
	shellScript := `
set -e
`
	if isDaemonInstalled() {
		// Uninstall it first.
		shellScript += fmt.Sprintf(`
"%s" uninstall
/bin/rm -f "%s" || true
`, exe, battSymlinkLocation)
	}

	output := &bytes.Buffer{}
	cmd := exec.Command("/usr/bin/osascript", "-e", fmt.Sprintf("do shell script \"%s\" with administrator privileges", escapeShellInAppleScript(shellScript)))
	cmd.Stderr = output
	cmd.Stdout = output
	err := cmd.Run()
	if err != nil {
		return pkgerrors.Wrapf(err, "failed to uninstall batt daemon: %s", output.String())
	}

	return nil
}

// installDaemon uninstalls existing daemons first (if exists), installs the batt daemon and creates a symlink to the executable.
func installDaemon(exe string) error {
	shellScript := `
set -e
`

	if isDaemonInstalled() {
		// Uninstall it first.
		shellScript += fmt.Sprintf(`
"%s" uninstall --no-reset-charging
/bin/rm -f "%s" || true
`, exe, battSymlinkLocation)
	}

	shellScript += fmt.Sprintf(`
"%s" install --allow-non-root-access
mkdir -p "$(dirname "%s")" # For whatever reason, some users don't have /usr/local/bin.
/bin/ln -sf "%s" "%s" || true
`, exe, battSymlinkLocation, exe, battSymlinkLocation)

	slog.Info("Installing daemon", "script", shellScript)

	output := &bytes.Buffer{}
	cmd := exec.Command("/usr/bin/osascript", "-e", fmt.Sprintf("do shell script \"%s\" with administrator privileges", escapeShellInAppleScript(shellScript)))
	cmd.Stderr = output
	cmd.Stdout = output
	err := cmd.Run()
	if err != nil {
		return pkgerrors.Wrapf(err, "failed to install batt daemon: %s", output.String())
	}

	return nil
}

func startAppAtBoot() error {
	if IsLoginItemRegistered() {
		slog.Info("Application is already registered to start at login")
		return nil
	}

	if err := RegisterLoginItem(); err != nil {
		return pkgerrors.Wrapf(err, "failed to register application to start at login")
	}
	slog.Info("Application registered to start at login")
	return nil
}
