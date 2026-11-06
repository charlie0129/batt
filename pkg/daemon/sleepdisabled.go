package daemon

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

// sleepHolds keeps the SleepDisabled setting on while any reason holds it and
// restores the user's own value once the last hold is gone.
type sleepHolds struct {
	mu      sync.Mutex
	setting SleepSetting

	// holds are the reasons currently holding sleep disabled, keyed by
	// reason id. Holds are idempotent per reason rather than counted: batt's
	// call sites are not transition-guarded -- several of them disable adapter
	// input unconditionally (calibration restore, cancel, the /adapter
	// endpoint) -- and a counter would accumulate holds that no matching enable
	// ever releases, leaving the Mac permanently unable to sleep.
	holds map[string]bool

	// previous is the value to restore once the last hold is gone, i.e. the
	// user's own setting from before batt touched it.
	previous bool

	// path holds the pending-restore snapshot. Read and written only with mu
	// held. Empty disables persistence.
	path string
}

func newSleepHolds(setting SleepSetting) *sleepHolds {
	return &sleepHolds{setting: setting, holds: map[string]bool{}}
}

type sleepDisabledSnapshot struct {
	Previous *bool `json:"previous"`
}

// init configures and validates the pending-restore snapshot. It deliberately
// does not restore it yet: startup must first read the adapter state. If
// adapter input is still disabled and the option is enabled, restoring sleep
// even briefly would immediately end Clamshell mode.
func (s *sleepHolds) init(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.path = path
	_, _, err := s.loadSnapshotLocked()
	if err != nil {
		logrus.WithError(err).Error("unreadable or malformed sleep snapshot found; preserving file for recovery")
	}
	return err
}

// RecoverSleepDisabled restores a pending snapshot without applying adapter
// policy. It is used during uninstall, after the daemon has been stopped and
// adapter input has been restored.
func RecoverSleepDisabled(path string) error {
	return recoverSleepDisabled(path, iokitSleepSetting{})
}

func recoverSleepDisabled(path string, setting SleepSetting) error {
	s := newSleepHolds(setting)
	s.path = path
	return s.restorePending()
}

// restorePending restores the snapshot a previous run left behind, if any.
func (s *sleepHolds) restorePending() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restorePendingLocked()
}

func (s *sleepHolds) restorePendingLocked() error {
	snapshot, ok, err := s.loadSnapshotLocked()
	if err != nil || !ok {
		return err
	}
	if err := s.setting.SetDisabled(*snapshot.Previous); err != nil {
		return err
	}
	logrus.Infof("restored SleepDisabled=%t left behind by a previous run", *snapshot.Previous)
	return s.clearSnapshotLocked()
}

// holding reports whether reason currently holds sleep disabled.
func (s *sleepHolds) holding(reason string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.holds[reason]
}

// count returns the number of active holds.
func (s *sleepHolds) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.holds)
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

// loadSnapshotLocked reads a pending snapshot, if any.
// A malformed or unreadable file is preserved for recovery and returned as a
// *sleepSnapshotError.
func (s *sleepHolds) loadSnapshotLocked() (sleepDisabledSnapshot, bool, error) {
	var snapshot sleepDisabledSnapshot

	if s.path == "" {
		return snapshot, false, nil
	}

	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return snapshot, false, nil
		}
		logrus.WithError(err).Warn("failed to read sleep-disabled state")
		return snapshot, false, &sleepSnapshotError{path: s.path, err: err}
	}

	if err := json.Unmarshal(b, &snapshot); err != nil {
		logrus.WithError(err).Warn("malformed sleep-disabled state")
		return snapshot, false, &sleepSnapshotError{path: s.path, err: fmt.Errorf("malformed sleep-disabled state: %w", err)}
	}

	if snapshot.Previous == nil {
		err := fmt.Errorf("missing 'previous' field in sleep-disabled state")
		logrus.WithError(err).Warn("invalid sleep-disabled state")
		return snapshot, false, &sleepSnapshotError{path: s.path, err: err}
	}

	return snapshot, true, nil
}

func (s *sleepHolds) persistSnapshotLocked(previous bool) error {
	if s.path == "" {
		return fmt.Errorf("sleep-disabled snapshot path is not configured")
	}
	b, err := json.Marshal(sleepDisabledSnapshot{Previous: &previous})
	if err != nil {
		return fmt.Errorf("failed to marshal sleep-disabled state: %w", err)
	}

	dir := filepath.Dir(s.path)
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

	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("failed to rename temp file to %s: %w", s.path, err)
	}

	if parentDir, err := os.Open(dir); err == nil {
		_ = parentDir.Sync()
		_ = parentDir.Close()
	}

	cleanup = false
	return nil
}

func (s *sleepHolds) clearSnapshotLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		logrus.WithError(err).Warn("failed to remove sleep-disabled state")
		return err
	}
	return nil
}

// hold suppresses all sleep, including lid-close sleep, on behalf of reason.
// Holding a reason that is already held does nothing.
func (s *sleepHolds) hold(reason string) error {
	_, err := s.acquire(reason)
	return err
}

// acquire is hold that also reports whether this call took the hold. A caller
// that fails afterwards must roll back only a hold it took itself: an earlier
// call may still need the one it took.
func (s *sleepHolds) acquire(reason string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.holds[reason] {
		return false, nil
	}

	if len(s.holds) == 0 {
		if err := s.takeFirstHoldLocked(); err != nil {
			return false, err
		}
	}

	s.holds[reason] = true
	return true, nil
}

// takeFirstHoldLocked records what to restore and disables sleep.
func (s *sleepHolds) takeFirstHoldLocked() error {
	// A snapshot on disk means an earlier restore did not complete. Its value is
	// the user's original setting; the live setting is batt's leftover and must
	// not be mistaken for user intent.
	snapshot, ok, err := s.loadSnapshotLocked()
	if err != nil {
		return fmt.Errorf("cannot acquire sleep hold: existing snapshot is unreadable or malformed: %w", err)
	}

	if ok {
		s.previous = *snapshot.Previous
	} else {
		previous, err := s.setting.Disabled()
		if err != nil {
			return err
		}
		if err := s.persistSnapshotLocked(previous); err != nil {
			return fmt.Errorf("failed to persist sleep-disabled snapshot: %w", err)
		}
		s.previous = previous
	}

	if s.previous {
		// The user disabled sleep themselves. Nothing to change, nothing to
		// restore later.
		return nil
	}

	if err := s.setting.SetDisabled(true); err != nil {
		if !ok {
			_ = s.clearSnapshotLocked()
		}
		return err
	}

	return nil
}

// release drops reason's hold. The system setting is restored once the last
// hold is gone. On failure the hold is kept, so ownership is not lost.
func (s *sleepHolds) release(reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.holds[reason] {
		return nil
	}

	delete(s.holds, reason)
	if len(s.holds) > 0 {
		return nil
	}

	if err := s.restoreLocked(); err != nil {
		s.holds[reason] = true
		return err
	}

	return nil
}

// releaseAll drops every hold, for daemon shutdown.
func (s *sleepHolds) releaseAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.holds) == 0 {
		return nil
	}

	if err := s.restoreLocked(); err != nil {
		return err
	}

	s.holds = map[string]bool{}
	return nil
}

func (s *sleepHolds) restoreLocked() error {
	if s.previous {
		// Sleep was already disabled before batt held it. Leave it alone.
		// Keep the original value until the snapshot is removed: a failed
		// removal must not make a later retry write SleepDisabled=false.
		if err := s.clearSnapshotLocked(); err != nil {
			return err
		}
		s.previous = false
		return nil
	}

	if err := s.setting.SetDisabled(false); err != nil {
		// Keep the snapshot so the next daemon start restores it.
		return err
	}

	return s.clearSnapshotLocked()
}

// disableAdapter cuts adapter input. Doing so makes macOS treat the Mac as
// running on battery, which ends Clamshell mode and immediately sleeps the
// external display. With prevent-sleep-on-adapter-disable enabled, suppress
// sleep first so Clamshell mode survives.
//
// Failing to take the hold aborts the operation rather than cutting power
// anyway: the setting exists precisely to keep the display alive, and silently
// proceeding would blank it.
func (d *Daemon) disableAdapter() error {
	d.adapterPolicyMu.Lock()
	defer d.adapterPolicyMu.Unlock()

	// The main loop and calibration call this on every cycle, also when the
	// adapter is already cut. Roll back only a hold this call took itself.
	tookHold := false
	if d.conf.PreventSleepOnAdapterDisable() {
		took, err := d.holds.acquire(sleepHoldAdapter)
		if err != nil {
			return fmt.Errorf("failed to disable sleep before cutting adapter input: %w", err)
		}
		tookHold = took
	}

	if err := d.smc.DisableAdapter(); err != nil {
		if tookHold {
			if releaseErr := d.holds.release(sleepHoldAdapter); releaseErr != nil {
				logrus.WithError(releaseErr).Error("failed to restore sleep after adapter disable failed")
				return fmt.Errorf("adapter disable failed (%w); sleep hold rollback also failed: %v", err, releaseErr)
			}
		}
		return err
	}

	return nil
}

// enableAdapter restores adapter input and releases the hold, if one is held.
// The release is unconditional on the setting: the setting may have been
// switched off while the hold was active, and the hold must still go.
func (d *Daemon) enableAdapter() error {
	d.adapterPolicyMu.Lock()
	defer d.adapterPolicyMu.Unlock()

	if err := d.smc.EnableAdapter(); err != nil {
		return err
	}

	if err := d.holds.release(sleepHoldAdapter); err != nil {
		logrus.WithError(err).Error("failed to restore sleep after enabling adapter")
		return fmt.Errorf("failed to restore sleep after enabling adapter: %w", err)
	}

	return nil
}

// restoreAdapterAfterPolicyError restores wall power before reporting a failed
// sleep reconciliation. Never continue with the adapter cut and protection
// missing; leave the snapshot intact if restoration itself fails.
func (d *Daemon) restoreAdapterAfterPolicyError(policyErr error) error {
	if err := d.enableAdapter(); err != nil {
		return fmt.Errorf("adapter sleep policy failed (%v); failed to restore wall power: %w", policyErr, err)
	}
	return fmt.Errorf("adapter sleep policy failed; wall power restored: %w", policyErr)
}

// adapterSleepPolicyCapable includes adapter mode, which owns the adapter for
// charge limiting but deliberately hides manual adapter controls from the API.
func (d *Daemon) adapterSleepPolicyCapable() bool {
	return d.getCapabilities().Supports(compatibility.FeatureAdapterSleepPolicy)
}

// reconcileAdapterSleepPolicy inspects the actual adapter state and ensures that
// the sleep hold matches policy:
//   - if the adapter is disabled and prevent-sleep-on-adapter-disable is enabled,
//     the hold is acquired;
//   - if the adapter is enabled, or the setting is disabled, the hold is released.
//
// Reconciliation is serialized by adapterPolicyMu and is idempotent.
func (d *Daemon) reconcileAdapterSleepPolicy() error {
	d.adapterPolicyMu.Lock()
	defer d.adapterPolicyMu.Unlock()

	if !d.adapterSleepPolicyCapable() {
		return nil
	}

	adapterEnabled, err := d.smc.IsAdapterEnabled()
	if err != nil {
		return fmt.Errorf("failed to check adapter state during sleep reconciliation: %w", err)
	}

	settingEnabled := d.conf.PreventSleepOnAdapterDisable()

	if !adapterEnabled && settingEnabled {
		if err := d.holds.hold(sleepHoldAdapter); err != nil {
			return fmt.Errorf("failed to acquire sleep hold for disabled adapter: %w", err)
		}
		return nil
	}

	if err := d.holds.release(sleepHoldAdapter); err != nil {
		return fmt.Errorf("failed to release sleep hold: %w", err)
	}

	// A previous daemon may have left a snapshot without any in-memory hold.
	// Once adapter policy no longer needs protection, finish that recovery.
	if err := d.holds.restorePending(); err != nil {
		return fmt.Errorf("failed to restore pending sleep-disabled state: %w", err)
	}

	return nil
}
