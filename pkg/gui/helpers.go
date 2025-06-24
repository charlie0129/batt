package gui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	pkgerrors "github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// #cgo CFLAGS: -x objective-c
// #cgo LDFLAGS: -framework Cocoa -framework ServiceManagement
// #import <Cocoa/Cocoa.h>
// #import <ServiceManagement/ServiceManagement.h>
//
// bool registerAppWithSMAppService(void) {
//     if (@available(macOS 13.0, *)) {
//         NSError *error = nil;
//         SMAppService *service = [SMAppService mainAppService];
//         BOOL success = [service registerAndReturnError:&error];
//
//         if (!success && error) {
//             NSLog(@"Failed to register login item: %@", error);
//             return false;
//         }
//         return success;
//     } else {
//         NSLog(@"SMAppService not available on this macOS version");
//         return false;
//     }
// }
//
// bool unregisterAppWithSMAppService(void) {
//     if (@available(macOS 13.0, *)) {
//         NSError *error = nil;
//         SMAppService *service = [SMAppService mainAppService];
//         BOOL success = [service unregisterAndReturnError:&error];
//
//         if (!success && error) {
//             NSLog(@"Failed to unregister login item: %@", error);
//             return false;
//         }
//         return success;
//     } else {
//         NSLog(@"SMAppService not available on this macOS version");
//         return false;
//     }
// }
//
// bool isRegisteredWithSMAppService(void) {
//     if (@available(macOS 13.0, *)) {
//         SMAppService *service = [SMAppService mainAppService];
//         return [service status] == SMAppServiceStatusEnabled;
//     }
//     return false;
// }
import "C"

// RegisterLoginItem registers the application to start at login using SMAppService
func RegisterLoginItem() error {
	logrus.Info("Registering application to start at login")

	if C.registerAppWithSMAppService() == C.bool(true) {
		logrus.Info("Successfully registered as login item")
		return nil
	}

	return fmt.Errorf("failed to register application as login item")
}

// UnregisterLoginItem removes the application from login items
func UnregisterLoginItem() error {
	logrus.Info("Removing application from login items")

	if C.unregisterAppWithSMAppService() == C.bool(true) {
		logrus.Info("Successfully unregistered login item")
		return nil
	}

	return fmt.Errorf("failed to unregister application as login item")
}

// IsLoginItemRegistered checks if the application is registered as a login item
func IsLoginItemRegistered() bool {
	return bool(C.isRegisteredWithSMAppService())
}

var (
	battSymlinkLocation = "/usr/local/bin/batt"
)

func isDaemonInstalled() bool {
	plistPath := "/Library/LaunchDaemons/cc.chlc.batt.plist"
	_, err := os.Stat(plistPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		} else {
			logrus.Warnf("Failed to check if %s exists: %s", plistPath, err)
			return false
		}
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
/bin/rm -f "%s"
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
/bin/rm -f "%s"
`, exe, battSymlinkLocation)
	}

	shellScript += fmt.Sprintf(`
"%s" install --allow-non-root-access
/bin/ln -sf "%s" "%s"
`, exe, exe, battSymlinkLocation)

	logrus.WithField("script", shellScript).Info("Installing daemon")

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
		logrus.Info("Application is already registered to start at login")
		return nil
	}

	if err := RegisterLoginItem(); err != nil {
		return pkgerrors.Wrapf(err, "failed to register application to start at login")
	}
	logrus.Info("Application registered to start at login")
	return nil
}
