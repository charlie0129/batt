package daemon

/*
#cgo LDFLAGS: -framework CoreFoundation -framework IOKit

#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOReturn.h>

// These three are declared in Apple's IOKitUser sources (IOPMLibPrivate.h) but
// are not part of the public SDK. They are exported by IOKit.framework at
// runtime -- pmset(8) uses them to implement `pmset disablesleep`. Declaring
// them here mirrors what Battery Toolkit does in its IOPMPrivate module
// (BSD-3-Clause, https://github.com/mhaeuser/Battery-Toolkit).
#define kIOPMSleepDisabledKey CFSTR("SleepDisabled")
CFDictionaryRef IOPMCopySystemPowerSettings(void);
IOReturn IOPMSetSystemPowerSetting(CFStringRef key, CFTypeRef value);

// Returns 1 if sleep is disabled, 0 if it is not, -1 on failure.
static int battGetSleepDisabled(void) {
	CFDictionaryRef settings = IOPMCopySystemPowerSettings();
	if (settings == NULL) {
		return -1;
	}

	int result = 0;
	CFTypeRef value = CFDictionaryGetValue(settings, kIOPMSleepDisabledKey);
	if (value != NULL && CFGetTypeID(value) == CFBooleanGetTypeID()) {
		result = CFBooleanGetValue((CFBooleanRef)value) ? 1 : 0;
	}

	CFRelease(settings);
	return result;
}

static IOReturn battSetSleepDisabled(int disabled) {
	return IOPMSetSystemPowerSetting(
		kIOPMSleepDisabledKey,
		disabled ? kCFBooleanTrue : kCFBooleanFalse
	);
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/compatibility"
)

// SleepDisabled is a global, persistent system power setting. Unlike a power
// assertion it also suppresses lid-close sleep, which is what Clamshell mode
// needs when adapter input is cut: macOS ends Clamshell mode as soon as it
// believes the Mac runs on battery, and the external display sleeps.
//
// Because the setting outlives the process, the value to restore is written to
// disk before the setting is changed and restored on the next daemon start.

// sleepHoldAdapter is the reason id used while adapter input is disabled.
const sleepHoldAdapter = "adapter-disabled"

var (
	sleepDisabledMu sync.Mutex
	adapterPolicyMu sync.Mutex

	// sleepHolds are the reasons currently holding sleep disabled, keyed by
	// reason id. Holds are idempotent per reason rather than counted: batt's
	// call sites are not transition-guarded -- several of them disable adapter
	// input unconditionally (calibration restore, cancel, the /adapter
	// endpoint) -- and a counter would accumulate holds that no matching enable
	// ever releases, leaving the Mac permanently unable to sleep.
	sleepHolds = map[string]bool{}

	// sleepDisabledPrevious is the value to restore once the last hold is gone,
	// i.e. the user's own setting from before batt touched it.
	sleepDisabledPrevious bool

	// sleepDisabledPath holds the pending-restore snapshot. Read and written
	// only with sleepDisabledMu held.
	sleepDisabledPath string

	// Seams for tests.
	getSleepDisabled  = getSleepDisabledSetting
	setSleepDisabled  = setSleepDisabledSetting
	rawDisableAdapter = func() error { return smcConn.DisableAdapter() }
	rawEnableAdapter  = func() error { return smcConn.EnableAdapter() }
)

type sleepDisabledSnapshot struct {
	Previous *bool `json:"previous"`
}

func getSleepDisabledSetting() (bool, error) {
	result := C.battGetSleepDisabled()
	if result < 0 {
		return false, fmt.Errorf("IOPMCopySystemPowerSettings returned NULL")
	}
	return result == 1, nil
}

func setSleepDisabledSetting(disabled bool) error {
	var arg C.int
	if disabled {
		arg = 1
	}
	status := C.battSetSleepDisabled(arg)
	if status != C.kIOReturnSuccess {
		return fmt.Errorf("IOPMSetSystemPowerSetting(SleepDisabled=%t) failed: 0x%x", disabled, uint32(status))
	}
	return nil
}

// initSleepDisabledState configures and validates the pending-restore snapshot.
// It deliberately does not restore it yet: startup must first read the adapter
// state. If adapter input is still disabled and the option is enabled, restoring
// sleep even briefly would immediately end Clamshell mode.
func initSleepDisabledState(path string) error {
	sleepDisabledMu.Lock()
	defer sleepDisabledMu.Unlock()

	sleepDisabledPath = path
	_, _, err := loadSleepDisabledSnapshotLocked()
	if err != nil {
		logrus.WithError(err).Error("unreadable or malformed sleep snapshot found; preserving file for recovery")
	}
	return err
}

// RecoverSleepDisabled restores a pending snapshot without applying adapter
// policy. It is used during uninstall, after the daemon has been stopped and
// adapter input has been restored.
func RecoverSleepDisabled(path string) error {
	sleepDisabledMu.Lock()
	defer sleepDisabledMu.Unlock()

	sleepDisabledPath = path
	return restorePendingSleepDisabledLocked()
}

func restorePendingSleepDisabled() error {
	sleepDisabledMu.Lock()
	defer sleepDisabledMu.Unlock()
	return restorePendingSleepDisabledLocked()
}

func restorePendingSleepDisabledLocked() error {
	snapshot, ok, err := loadSleepDisabledSnapshotLocked()
	if err != nil || !ok {
		return err
	}
	if err := setSleepDisabled(*snapshot.Previous); err != nil {
		return err
	}
	logrus.Infof("restored SleepDisabled=%t left behind by a previous run", *snapshot.Previous)
	return clearSleepDisabledSnapshotLocked()
}

// sleepSnapshotError marks a snapshot file that exists but cannot be used. The
// file stays in place for manual recovery.
type sleepSnapshotError struct {
	path string
	err  error
}

func (e *sleepSnapshotError) Error() string {
	if strings.Contains(e.err.Error(), e.path) {
		return e.err.Error()
	}
	return fmt.Sprintf("%s: %v", e.path, e.err)
}

func (e *sleepSnapshotError) Unwrap() error { return e.err }

// loadSleepDisabledSnapshotLocked reads a pending snapshot, if any.
// A malformed or unreadable file is preserved for recovery and returned as a
// *sleepSnapshotError.
func loadSleepDisabledSnapshotLocked() (sleepDisabledSnapshot, bool, error) {
	var snapshot sleepDisabledSnapshot

	if sleepDisabledPath == "" {
		return snapshot, false, nil
	}

	b, err := os.ReadFile(sleepDisabledPath)
	if err != nil {
		if os.IsNotExist(err) {
			return snapshot, false, nil
		}
		logrus.WithError(err).Warn("failed to read sleep-disabled state")
		return snapshot, false, &sleepSnapshotError{path: sleepDisabledPath, err: err}
	}

	if err := json.Unmarshal(b, &snapshot); err != nil {
		logrus.WithError(err).Warn("malformed sleep-disabled state")
		return snapshot, false, &sleepSnapshotError{path: sleepDisabledPath, err: fmt.Errorf("malformed sleep-disabled state: %w", err)}
	}

	if snapshot.Previous == nil {
		err := fmt.Errorf("missing 'previous' field in sleep-disabled state")
		logrus.WithError(err).Warn("invalid sleep-disabled state")
		return snapshot, false, &sleepSnapshotError{path: sleepDisabledPath, err: err}
	}

	return snapshot, true, nil
}

func persistSleepDisabledSnapshotLocked(previous bool) error {
	if sleepDisabledPath == "" {
		return fmt.Errorf("sleep-disabled snapshot path is not configured")
	}
	b, err := json.Marshal(sleepDisabledSnapshot{Previous: &previous})
	if err != nil {
		return fmt.Errorf("failed to marshal sleep-disabled state: %w", err)
	}

	dir := filepath.Dir(sleepDisabledPath)
	tmpFile, err := os.CreateTemp(dir, "batt.sleep.*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file for sleep-disabled state: %w", err)
	}
	tmpName := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to chmod temp file: %w", err)
	}

	if _, err := tmpFile.Write(b); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpName, sleepDisabledPath); err != nil {
		return fmt.Errorf("failed to rename temp file to %s: %w", sleepDisabledPath, err)
	}

	if parentDir, err := os.Open(dir); err == nil {
		_ = parentDir.Sync()
		_ = parentDir.Close()
	}

	cleanup = false
	return nil
}

func clearSleepDisabledSnapshotLocked() error {
	if sleepDisabledPath == "" {
		return nil
	}
	if err := os.Remove(sleepDisabledPath); err != nil && !os.IsNotExist(err) {
		logrus.WithError(err).Warn("failed to remove sleep-disabled state")
		return err
	}
	return nil
}

// holdSleep suppresses all sleep, including lid-close sleep, on behalf of
// reason. Holding a reason that is already held does nothing.
func holdSleep(reason string) error {
	_, err := acquireSleepHold(reason)
	return err
}

// acquireSleepHold is holdSleep that also reports whether this call took the
// hold. A caller that fails afterwards must roll back only a hold it took
// itself: an earlier call may still need the one it took.
func acquireSleepHold(reason string) (bool, error) {
	sleepDisabledMu.Lock()
	defer sleepDisabledMu.Unlock()

	if sleepHolds[reason] {
		return false, nil
	}

	if len(sleepHolds) == 0 {
		if err := takeFirstHoldLocked(); err != nil {
			return false, err
		}
	}

	sleepHolds[reason] = true
	return true, nil
}

// takeFirstHoldLocked records what to restore and disables sleep.
func takeFirstHoldLocked() error {
	// A snapshot on disk means an earlier restore did not complete. Its value is
	// the user's original setting; the live setting is batt's leftover and must
	// not be mistaken for user intent.
	snapshot, ok, err := loadSleepDisabledSnapshotLocked()
	if err != nil {
		return fmt.Errorf("cannot acquire sleep hold: existing snapshot is unreadable or malformed: %w", err)
	}

	if ok {
		sleepDisabledPrevious = *snapshot.Previous
	} else {
		previous, err := getSleepDisabled()
		if err != nil {
			return err
		}
		if err := persistSleepDisabledSnapshotLocked(previous); err != nil {
			return fmt.Errorf("failed to persist sleep-disabled snapshot: %w", err)
		}
		sleepDisabledPrevious = previous
	}

	if sleepDisabledPrevious {
		// The user disabled sleep themselves. Nothing to change, nothing to
		// restore later.
		return nil
	}

	if err := setSleepDisabled(true); err != nil {
		if !ok {
			_ = clearSleepDisabledSnapshotLocked()
		}
		return err
	}

	return nil
}

// releaseSleep drops reason's hold. The system setting is restored once the last
// hold is gone. On failure the hold is kept, so ownership is not lost.
func releaseSleep(reason string) error {
	sleepDisabledMu.Lock()
	defer sleepDisabledMu.Unlock()

	if !sleepHolds[reason] {
		return nil
	}

	delete(sleepHolds, reason)
	if len(sleepHolds) > 0 {
		return nil
	}

	if err := restoreSleepLocked(); err != nil {
		sleepHolds[reason] = true
		return err
	}

	return nil
}

// releaseAllSleepHolds drops every hold, for daemon shutdown.
func releaseAllSleepHolds() error {
	sleepDisabledMu.Lock()
	defer sleepDisabledMu.Unlock()

	if len(sleepHolds) == 0 {
		return nil
	}

	if err := restoreSleepLocked(); err != nil {
		return err
	}

	sleepHolds = map[string]bool{}
	return nil
}

func restoreSleepLocked() error {
	if sleepDisabledPrevious {
		// Sleep was already disabled before batt held it. Leave it alone.
		// Keep the original value until the snapshot is removed: a failed
		// removal must not make a later retry write SleepDisabled=false.
		if err := clearSleepDisabledSnapshotLocked(); err != nil {
			return err
		}
		sleepDisabledPrevious = false
		return nil
	}

	if err := setSleepDisabled(false); err != nil {
		// Keep the snapshot so the next daemon start restores it.
		return err
	}

	return clearSleepDisabledSnapshotLocked()
}

// disableAdapterWithSleepPolicy cuts adapter input. Doing so makes macOS treat
// the Mac as running on battery, which ends Clamshell mode and immediately
// sleeps the external display. With prevent-sleep-on-adapter-disable enabled,
// suppress sleep first so Clamshell mode survives.
//
// Failing to take the hold aborts the operation rather than cutting power
// anyway: the setting exists precisely to keep the display alive, and silently
// proceeding would blank it.
func disableAdapterWithSleepPolicy() error {
	adapterPolicyMu.Lock()
	defer adapterPolicyMu.Unlock()

	// The main loop and calibration call this on every cycle, also when the
	// adapter is already cut. Roll back only a hold this call took itself.
	tookHold := false
	if conf != nil && conf.PreventSleepOnAdapterDisable() {
		took, err := acquireSleepHold(sleepHoldAdapter)
		if err != nil {
			return fmt.Errorf("failed to disable sleep before cutting adapter input: %w", err)
		}
		tookHold = took
	}

	if err := rawDisableAdapter(); err != nil {
		if tookHold {
			if releaseErr := releaseSleep(sleepHoldAdapter); releaseErr != nil {
				logrus.WithError(releaseErr).Error("failed to restore sleep after adapter disable failed")
				return fmt.Errorf("adapter disable failed (%w); sleep hold rollback also failed: %v", err, releaseErr)
			}
		}
		return err
	}

	return nil
}

// enableAdapterWithSleepPolicy restores adapter input and releases the hold, if
// one is held. The release is unconditional on the setting: the setting may have
// been switched off while the hold was active, and the hold must still go.
func enableAdapterWithSleepPolicy() error {
	adapterPolicyMu.Lock()
	defer adapterPolicyMu.Unlock()

	if err := rawEnableAdapter(); err != nil {
		return err
	}

	if err := releaseSleep(sleepHoldAdapter); err != nil {
		logrus.WithError(err).Error("failed to restore sleep after enabling adapter")
		return fmt.Errorf("failed to restore sleep after enabling adapter: %w", err)
	}

	return nil
}

// restoreAdapterAfterPolicyError restores wall power before reporting a failed
// sleep reconciliation. Never continue with the adapter cut and protection
// missing; leave the snapshot intact if restoration itself fails.
func restoreAdapterAfterPolicyError(policyErr error) error {
	if err := smcEnableAdapter(); err != nil {
		return fmt.Errorf("adapter sleep policy failed (%v); failed to restore wall power: %w", policyErr, err)
	}
	return fmt.Errorf("adapter sleep policy failed; wall power restored: %w", policyErr)
}

// adapterSleepPolicyCapable includes adapter mode, which owns the adapter for
// charge limiting but deliberately hides manual adapter controls from the API.
func adapterSleepPolicyCapable() bool {
	return getCapabilities().Supports(compatibility.FeatureAdapterSleepPolicy)
}

// reconcileAdapterSleepPolicy inspects the actual adapter state and ensures that
// the sleep hold matches policy:
//   - if the adapter is disabled and prevent-sleep-on-adapter-disable is enabled,
//     the hold is acquired;
//   - if the adapter is enabled, or the setting is disabled, the hold is released.
//
// Reconciliation is serialized by adapterPolicyMu and is idempotent.
func reconcileAdapterSleepPolicy() error {
	adapterPolicyMu.Lock()
	defer adapterPolicyMu.Unlock()

	if !adapterSleepPolicyCapable() {
		return nil
	}

	adapterEnabled, err := smcIsAdapterEnabled()
	if err != nil {
		return fmt.Errorf("failed to check adapter state during sleep reconciliation: %w", err)
	}

	settingEnabled := conf != nil && conf.PreventSleepOnAdapterDisable()

	if !adapterEnabled && settingEnabled {
		if err := holdSleep(sleepHoldAdapter); err != nil {
			return fmt.Errorf("failed to acquire sleep hold for disabled adapter: %w", err)
		}
		return nil
	}

	if err := releaseSleep(sleepHoldAdapter); err != nil {
		return fmt.Errorf("failed to release sleep hold: %w", err)
	}

	// A previous daemon may have left a snapshot without any in-memory hold.
	// Once adapter policy no longer needs protection, finish that recovery.
	if err := restorePendingSleepDisabled(); err != nil {
		return fmt.Errorf("failed to restore pending sleep-disabled state: %w", err)
	}

	return nil
}
