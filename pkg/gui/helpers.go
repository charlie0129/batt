package gui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime/cgo"
	"strings"
	"unsafe"

	pkgerrors "github.com/pkg/errors"
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/sirupsen/logrus"
)

// #cgo CFLAGS: -x objective-c
// #cgo LDFLAGS: -framework Cocoa -framework ServiceManagement -framework CoreFoundation
// #include <stdint.h>
// #include <stdbool.h>
// #include <stdlib.h>
// // C/ObjC functions are implemented in bridge.m; only prototypes here.
// void *batt_attachMenuObserver(uintptr_t menuPtr, uintptr_t handle);
// void batt_releaseMenuObserver(void *obsPtr);
// bool registerAppWithSMAppService(void);
// bool unregisterAppWithSMAppService(void);
// bool isRegisteredWithSMAppService(void);
// void batt_showNotification(const char* title, const char* body);
import "C"

//export battMenuWillOpen
func battMenuWillOpen(h C.uintptr_t) {
	defer func() {
		if r := recover(); r != nil {
			logrus.Errorf("panic in battMenuWillOpen: %v", r)
		}
	}()
	handle := cgo.Handle(h)
	if v := handle.Value(); v != nil {
		if c, ok := v.(*menuController); ok {
			c.onWillOpen()
		}
	}
}

//export battMenuDidClose
func battMenuDidClose(h C.uintptr_t) {
	defer func() {
		if r := recover(); r != nil {
			logrus.Errorf("panic in battMenuDidClose: %v", r)
		}
	}()
	handle := cgo.Handle(h)
	if v := handle.Value(); v != nil {
		if c, ok := v.(*menuController); ok {
			c.onDidClose()
		}
	}
}

//export battMenuTimerFired
func battMenuTimerFired(h C.uintptr_t) {
	defer func() {
		if r := recover(); r != nil {
			logrus.Errorf("panic in battMenuTimerFired: %v", r)
		}
	}()
	handle := cgo.Handle(h)
	if v := handle.Value(); v != nil {
		if c, ok := v.(*menuController); ok {
			c.onTimerTick()
		}
	}
}

// AttachPowerFlowObserver wires an Objective-C NSMenu notifications observer to a Go handle.
// It returns an opaque pointer retained on the ObjC side; call ReleasePowerFlowObserver to free.
func AttachPowerFlowObserver(menu appkit.Menu, h cgo.Handle) unsafe.Pointer {
	return C.batt_attachMenuObserver(C.uintptr_t(uintptr(menu.Ptr())), C.uintptr_t(h))
}

func ReleasePowerFlowObserver(ptr unsafe.Pointer) {
	C.batt_releaseMenuObserver(ptr)
}

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
		}
		logrus.Warnf("Failed to check if %s exists: %s", plistPath, err)
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

func showNotification(title, body string) {
	// Use UNUserNotificationCenter via Objective-C bridge. Best-effort, async.
	go func() {
		ctitle := C.CString(title)
		cbody := C.CString(body)
		defer C.free(unsafe.Pointer(ctitle))
		defer C.free(unsafe.Pointer(cbody))
		C.batt_showNotification(ctitle, cbody)
	}()
}

func showAlert(msg, body string) {
	alert := appkit.NewAlert()
	alert.SetIcon(appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("exclamationmark.triangle", "s"))
	alert.SetAlertStyle(appkit.AlertStyleWarning)
	alert.SetMessageText(msg)
	alert.SetInformativeText(body)
	alert.RunModal()
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
