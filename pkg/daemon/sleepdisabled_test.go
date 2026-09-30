package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlie0129/batt/pkg/compatibility"
)

// fakeSleepSetting replaces the IOKit calls with an in-memory value and records
// how often the setting was actually written.
type fakeSleepSetting struct {
	value         bool
	writes        int
	writtenValues []bool
	getErr        error
	setErr        error
	setErrFor     *bool // fail only when writing this value
}

func (f *fakeSleepSetting) get() (bool, error) {
	if f.getErr != nil {
		return false, f.getErr
	}
	return f.value, nil
}

func (f *fakeSleepSetting) set(disabled bool) error {
	if f.setErr != nil && (f.setErrFor == nil || *f.setErrFor == disabled) {
		return f.setErr
	}
	f.value = disabled
	f.writes++
	f.writtenValues = append(f.writtenValues, disabled)
	return nil
}

func stubSleepDisabled(t *testing.T, initial bool) *fakeSleepSetting {
	t.Helper()

	previousGet, previousSet := getSleepDisabled, setSleepDisabled
	t.Cleanup(func() {
		getSleepDisabled, setSleepDisabled = previousGet, previousSet
		sleepHolds = map[string]bool{}
		sleepDisabledPrevious = false
		sleepDisabledPath = ""
	})

	sleepHolds = map[string]bool{}
	sleepDisabledPrevious = false
	sleepDisabledPath = filepath.Join(t.TempDir(), "batt.sleep.json")

	fake := &fakeSleepSetting{value: initial}
	getSleepDisabled = fake.get
	setSleepDisabled = fake.set
	return fake
}

// stubAdapter replaces the raw SMC calls and tracks adapter state.
type fakeAdapter struct {
	enabled    bool
	disableErr error
	enableErr  error
}

func stubAdapter(t *testing.T, enabled bool) *fakeAdapter {
	t.Helper()

	previousDisable, previousEnable := rawDisableAdapter, rawEnableAdapter
	previousConf := conf
	previousIsAdapter := smcIsAdapterEnabled
	previousCap := capabilities
	previousEnableAdapter := smcEnableAdapter
	previousDisableAdapter := smcDisableAdapter
	smcEnableAdapter = enableAdapterWithSleepPolicy
	smcDisableAdapter = disableAdapterWithSleepPolicy
	t.Cleanup(func() {
		rawDisableAdapter, rawEnableAdapter = previousDisable, previousEnable
		conf = previousConf
		smcIsAdapterEnabled = previousIsAdapter
		capabilities = previousCap
		smcEnableAdapter = previousEnableAdapter
		smcDisableAdapter = previousDisableAdapter
	})

	capabilities.AdapterControl = true
	fake := &fakeAdapter{enabled: enabled}
	rawDisableAdapter = func() error {
		if fake.disableErr != nil {
			return fake.disableErr
		}
		fake.enabled = false
		return nil
	}
	rawEnableAdapter = func() error {
		if fake.enableErr != nil {
			return fake.enableErr
		}
		fake.enabled = true
		return nil
	}
	smcIsAdapterEnabled = func() (bool, error) {
		return fake.enabled, nil
	}
	return fake
}

// sleepPolicyConf is a config whose only relevant knob is the new setting.
type sleepPolicyConf struct {
	mockConf
	prevent bool
}

func (c *sleepPolicyConf) PreventSleepOnAdapterDisable() bool { return c.prevent }

// ---------------------------------------------------------------------------
// Hold bookkeeping
// ---------------------------------------------------------------------------

func TestHoldSleepIsIdempotentPerReason(t *testing.T) {
	// Regression: batt's call sites are not transition-guarded. Several disable
	// adapter input unconditionally, so the same reason arrives repeatedly. A
	// counted hold would never reach zero again.
	fake := stubSleepDisabled(t, false)

	for i := 0; i < 5; i++ {
		if err := holdSleep(sleepHoldAdapter); err != nil {
			t.Fatalf("holdSleep #%d: %v", i, err)
		}
	}
	if !fake.value {
		t.Fatal("sleep should be disabled")
	}
	if fake.writes != 1 {
		t.Fatalf("setting written %d times, want 1", fake.writes)
	}

	if err := releaseSleep(sleepHoldAdapter); err != nil {
		t.Fatalf("releaseSleep: %v", err)
	}
	if fake.value {
		t.Fatal("a single release must undo repeated holds of the same reason")
	}
	if len(sleepHolds) != 0 {
		t.Fatalf("holds left over: %v", sleepHolds)
	}
}

func TestIndependentReasonsEachHold(t *testing.T) {
	fake := stubSleepDisabled(t, false)

	if err := holdSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if err := holdSleep("some-other-reason"); err != nil {
		t.Fatal(err)
	}

	if err := releaseSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if !fake.value {
		t.Fatal("sleep must stay disabled while another reason holds")
	}

	if err := releaseSleep("some-other-reason"); err != nil {
		t.Fatal(err)
	}
	if fake.value {
		t.Fatal("sleep should be restored once the last reason is gone")
	}
}

func TestReleaseSleepHonoursUserSetting(t *testing.T) {
	fake := stubSleepDisabled(t, true) // user disabled sleep themselves

	if err := holdSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if fake.writes != 0 {
		t.Fatalf("nothing to write when already disabled, got %d writes", fake.writes)
	}

	if err := releaseSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if !fake.value {
		t.Fatal("must not re-enable sleep the user had disabled")
	}
}

func TestReleaseWithoutHoldIsNoop(t *testing.T) {
	fake := stubSleepDisabled(t, false)

	if err := releaseSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if fake.writes != 0 {
		t.Fatalf("unbalanced release wrote the setting %d times", fake.writes)
	}
}

func TestReleaseAllSleepHoldsDropsEveryReason(t *testing.T) {
	fake := stubSleepDisabled(t, false)

	for _, reason := range []string{sleepHoldAdapter, "a", "b"} {
		if err := holdSleep(reason); err != nil {
			t.Fatal(err)
		}
	}
	if err := releaseAllSleepHolds(); err != nil {
		t.Fatal(err)
	}
	if fake.value {
		t.Fatal("releaseAllSleepHolds must restore the setting")
	}
	if len(sleepHolds) != 0 {
		t.Fatalf("holds left over: %v", sleepHolds)
	}
}

func TestReleaseAllWithoutHoldsIsNoop(t *testing.T) {
	fake := stubSleepDisabled(t, false)

	if err := releaseAllSleepHolds(); err != nil {
		t.Fatal(err)
	}
	if fake.writes != 0 {
		t.Fatalf("wrote the setting %d times without holds", fake.writes)
	}
}

// ---------------------------------------------------------------------------
// Failure paths
// ---------------------------------------------------------------------------

func TestHoldSleepPropagatesReadFailure(t *testing.T) {
	fake := stubSleepDisabled(t, false)
	fake.getErr = errors.New("IOPMCopySystemPowerSettings returned NULL")

	if err := holdSleep(sleepHoldAdapter); err == nil {
		t.Fatal("expected the read failure to propagate")
	}
	if len(sleepHolds) != 0 {
		t.Fatal("no hold may be recorded when the read failed")
	}
	if _, err := os.Stat(sleepDisabledPath); !os.IsNotExist(err) {
		t.Fatal("no snapshot may be left behind when the read failed")
	}
}

func TestHoldSleepCleansUpAfterWriteFailure(t *testing.T) {
	fake := stubSleepDisabled(t, false)
	fake.setErr = errors.New("IOPMSetSystemPowerSetting failed")

	if err := holdSleep(sleepHoldAdapter); err == nil {
		t.Fatal("expected the write failure to propagate")
	}
	if len(sleepHolds) != 0 {
		t.Fatal("no hold may be recorded when the write failed")
	}
	if _, err := os.Stat(sleepDisabledPath); !os.IsNotExist(err) {
		t.Fatal("a failed hold must not leave a snapshot behind")
	}
}

func TestFailedReleaseKeepsHoldAndSnapshot(t *testing.T) {
	// If the restore write fails, ownership must not be dropped: otherwise the
	// next hold would snapshot batt's own leftover value as "user intent" and
	// sleep would stay disabled forever.
	fake := stubSleepDisabled(t, false)

	if err := holdSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}

	restoreValue := false
	fake.setErr = errors.New("IOPMSetSystemPowerSetting failed")
	fake.setErrFor = &restoreValue

	if err := releaseSleep(sleepHoldAdapter); err == nil {
		t.Fatal("expected the restore failure to propagate")
	}
	if !sleepHolds[sleepHoldAdapter] {
		t.Fatal("the hold must be kept when the restore failed")
	}
	if _, err := os.Stat(sleepDisabledPath); err != nil {
		t.Fatalf("snapshot must survive a failed restore: %v", err)
	}

	// Once the write works again, the retry restores and cleans up.
	fake.setErr = nil
	fake.setErrFor = nil
	if err := releaseSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if fake.value {
		t.Fatal("retry should have restored sleep")
	}
	if _, err := os.Stat(sleepDisabledPath); !os.IsNotExist(err) {
		t.Fatal("snapshot should be gone after a successful retry")
	}
}

func TestFailedSnapshotRemovalPreservesPreExistingSleepSetting(t *testing.T) {
	fake := stubSleepDisabled(t, true)
	if err := holdSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sleepDisabledPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sleepDisabledPath, 0o700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(sleepDisabledPath, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := releaseSleep(sleepHoldAdapter); err == nil {
		t.Fatal("expected snapshot removal to fail")
	}
	if !sleepHolds[sleepHoldAdapter] || !sleepDisabledPrevious || !fake.value {
		t.Fatal("failed removal must preserve the hold and original SleepDisabled=true")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := releaseSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if !fake.value || fake.writes != 0 {
		t.Fatal("retry must not change the user's original SleepDisabled=true")
	}
}

func TestPendingSnapshotWinsOverLiveValue(t *testing.T) {
	// A snapshot on disk means an earlier restore did not complete. The live
	// value is batt's leftover, not the user's setting, and must not be adopted.
	fake := stubSleepDisabled(t, true)

	if err := os.WriteFile(sleepDisabledPath, []byte(`{"previous":false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := holdSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if err := releaseSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if fake.value {
		t.Fatal("restore must use the pending snapshot, not the leftover live value")
	}
}

// ---------------------------------------------------------------------------
// Snapshot persistence
// ---------------------------------------------------------------------------

func TestSnapshotIsPersistedAndClearedAgain(t *testing.T) {
	stubSleepDisabled(t, false)
	path := sleepDisabledPath

	if err := holdSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot should exist while sleep is held: %v", err)
	}

	if err := releaseSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot should be gone after restore, got %v", err)
	}
}

func TestRecoverSleepDisabledRestoresSnapshotLeftBehind(t *testing.T) {
	fake := stubSleepDisabled(t, true) // daemon was killed with sleep disabled
	path := sleepDisabledPath

	if err := os.WriteFile(path, []byte(`{"previous":false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RecoverSleepDisabled(path); err != nil {
		t.Fatal(err)
	}

	if fake.value {
		t.Fatal("recovery must restore the pre-crash value")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot should be consumed, got %v", err)
	}
}

func TestRecoverSleepDisabledKeepsSnapshotWhenRestoreFails(t *testing.T) {
	fake := stubSleepDisabled(t, true)
	path := sleepDisabledPath
	fake.setErr = errors.New("IOPMSetSystemPowerSetting failed")

	if err := os.WriteFile(path, []byte(`{"previous":false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RecoverSleepDisabled(path); err == nil {
		t.Fatal("expected recovery to fail")
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot must survive so the next recovery can retry: %v", err)
	}
}

func TestStartupReconcileKeepsSleepDisabledWithoutGap(t *testing.T) {
	fake := stubSleepDisabled(t, true)
	adapter := stubAdapter(t, false)
	conf = &sleepPolicyConf{prevent: true}
	capabilities.AdapterControl = true
	path := sleepDisabledPath

	if err := os.WriteFile(path, []byte(`{"previous":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := initSleepDisabledState(path); err != nil {
		t.Fatal(err)
	}
	if err := reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}

	if adapter.enabled {
		t.Fatal("adapter should remain disabled")
	}
	if !fake.value || !sleepHolds[sleepHoldAdapter] {
		t.Fatal("startup must retain sleep protection for the disabled adapter")
	}
	for _, value := range fake.writtenValues {
		if !value {
			t.Fatal("startup briefly restored sleep before reacquiring the hold")
		}
	}
}

func TestReloadReconcilesSleepImmediately(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, false)
	conf = &sleepPolicyConf{prevent: true}

	if err := reconcileReloadedSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if adapter.enabled || !sleep.value || !sleepHolds[sleepHoldAdapter] {
		t.Fatal("reload must hold sleep before returning while wall power is cut")
	}
}

func TestStartupSleepPolicyFailureRestoresWallPower(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, false)
	conf = &sleepPolicyConf{prevent: true}
	sleep.setErr = errors.New("IOPM unavailable")

	if err := ensureStartupSleepPolicy(); err == nil {
		t.Fatal("startup must fail when sleep protection cannot be established")
	}
	if !adapter.enabled {
		t.Fatal("startup failure must restore wall power before returning")
	}
}

func TestStartupSleepPolicyFailureKeepsSnapshotWhenAdapterCannotRestore(t *testing.T) {
	stubSleepDisabled(t, false)
	adapter := stubAdapter(t, false)
	conf = &sleepPolicyConf{prevent: true}
	if err := os.WriteFile(sleepDisabledPath, []byte("malformed"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter.enableErr = errors.New("SMC unavailable")

	if err := ensureStartupSleepPolicy(); err == nil {
		t.Fatal("startup must fail when neither sleep protection nor wall power can be restored")
	}
	if adapter.enabled {
		t.Fatal("adapter mock must still be disabled")
	}
	if _, err := os.Stat(sleepDisabledPath); err != nil {
		t.Fatalf("failed recovery must preserve the snapshot: %v", err)
	}
}

// A broken note about the old sleep setting must not keep the daemon from
// starting. The charge limit does not depend on it. The file stays in place.
func TestStartupContinuesWhenSnapshotIsMalformed(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	stubAdapter(t, true)
	conf = &sleepPolicyConf{prevent: false}
	if err := os.WriteFile(sleepDisabledPath, []byte("{trunc"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ensureStartupSleepPolicy(); err != nil {
		t.Fatalf("a malformed snapshot must not stop the daemon: %v", err)
	}
	if b, err := os.ReadFile(sleepDisabledPath); err != nil || string(b) != "{trunc" {
		t.Fatalf("the malformed snapshot must stay untouched for manual recovery: %q, %v", b, err)
	}
	if sleep.writes != 0 {
		t.Fatalf("nothing may be written for a malformed snapshot, got %d", sleep.writes)
	}
}

func TestStartupContinuesWhenSnapshotIsMalformedWithoutAdapterControl(t *testing.T) {
	stubSleepDisabled(t, false)
	stubAdapter(t, true)
	capabilities = compatibility.Capabilities{}
	conf = &sleepPolicyConf{prevent: false}
	if err := os.WriteFile(sleepDisabledPath, []byte("{trunc"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ensureStartupSleepPolicy(); err != nil {
		t.Fatalf("a malformed snapshot must not stop the daemon: %v", err)
	}
	if _, err := os.Stat(sleepDisabledPath); err != nil {
		t.Fatalf("the malformed snapshot must be preserved: %v", err)
	}
}

func TestStartupWithMalformedSnapshotRestoresWallPowerAndContinues(t *testing.T) {
	// The adapter is cut and the feature is on, but no hold can be taken while
	// the snapshot is unreadable. Wall power comes back, and the daemon starts.
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, false)
	conf = &sleepPolicyConf{prevent: true}
	if err := os.WriteFile(sleepDisabledPath, []byte("{trunc"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ensureStartupSleepPolicy(); err != nil {
		t.Fatalf("a malformed snapshot must not stop the daemon: %v", err)
	}
	if !adapter.enabled {
		t.Fatal("wall power must come back when protection cannot be taken")
	}
	if sleep.writes != 0 {
		t.Fatalf("nothing may be written for a malformed snapshot, got %d", sleep.writes)
	}
	if _, err := os.Stat(sleepDisabledPath); err != nil {
		t.Fatalf("the malformed snapshot must be preserved: %v", err)
	}
}

func TestUnusableSnapshotErrorNamesTheFile(t *testing.T) {
	stubSleepDisabled(t, false)
	if err := os.WriteFile(sleepDisabledPath, []byte("{trunc"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := holdSleep("test-reason")
	if err == nil {
		t.Fatal("holdSleep must fail while the snapshot is malformed")
	}
	if !strings.Contains(err.Error(), sleepDisabledPath) {
		t.Fatalf("the error must name the file so the user can find it: %v", err)
	}
}

func TestInitPreservesMalformedSnapshot(t *testing.T) {
	fake := stubSleepDisabled(t, false)
	path := sleepDisabledPath

	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := initSleepDisabledState(path)
	if err == nil {
		t.Fatal("expected error on malformed snapshot")
	}

	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("a malformed snapshot must be preserved for recovery, got: %v", statErr)
	}
	if fake.writes != 0 {
		t.Fatalf("nothing should be written for a malformed snapshot, got %d", fake.writes)
	}
}

func TestInitPreservesMissingPreviousField(t *testing.T) {
	fake := stubSleepDisabled(t, false)
	path := sleepDisabledPath

	if err := os.WriteFile(path, []byte(`{"other": 123}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := initSleepDisabledState(path)
	if err == nil {
		t.Fatal("expected error on missing previous field")
	}

	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("snapshot with missing previous must be preserved, got: %v", statErr)
	}
	if fake.writes != 0 {
		t.Fatalf("nothing should be written when previous field is missing, got %d", fake.writes)
	}
}

func TestTakeFirstHoldFailsWhenSnapshotMalformed(t *testing.T) {
	fake := stubSleepDisabled(t, false)
	path := sleepDisabledPath

	if err := os.WriteFile(path, []byte(`{"previous": null}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := holdSleep("test-reason"); err == nil {
		t.Fatal("holdSleep must fail when existing snapshot is malformed")
	}

	if fake.writes != 0 {
		t.Fatalf("setSleepDisabled must not be called when snapshot is malformed, got %d", fake.writes)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("malformed snapshot must be preserved, got: %v", statErr)
	}
}

func TestTakeFirstHoldFailsWhenPersistenceFails(t *testing.T) {
	fake := stubSleepDisabled(t, false)
	// Point to a directory that cannot be created or written to
	sleepDisabledPath = filepath.Join(t.TempDir(), "nonexistent", "batt.sleep.json")

	if err := holdSleep("test-reason"); err == nil {
		t.Fatal("holdSleep must fail when persistence fails")
	}

	if fake.writes != 0 {
		t.Fatalf("setSleepDisabled must not be called when persistence fails, got %d", fake.writes)
	}
	if len(sleepHolds) != 0 {
		t.Fatalf("no holds should be recorded on persistence failure, got %v", sleepHolds)
	}
}

func TestReleaseAllSleepHoldsPreservesHoldsOnRestoreFailure(t *testing.T) {
	fake := stubSleepDisabled(t, false)

	if err := holdSleep("reason-a"); err != nil {
		t.Fatal(err)
	}
	if err := holdSleep("reason-b"); err != nil {
		t.Fatal(err)
	}

	fake.setErr = errors.New("IOPMSetSystemPowerSetting failed")

	if err := releaseAllSleepHolds(); err == nil {
		t.Fatal("expected releaseAllSleepHolds to fail when restore fails")
	}

	if len(sleepHolds) == 0 {
		t.Fatal("releaseAllSleepHolds must retain holds in memory when restore fails")
	}
}

func TestAtomicSnapshotWritePreservesValidSnapshotOnFailure(t *testing.T) {
	dir := t.TempDir()
	validPath := filepath.Join(dir, "batt.sleep.json")
	if err := os.WriteFile(validPath, []byte(`{"previous":false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Make directory read-only so creating a temporary file fails
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()

	sleepDisabledPath = validPath
	err := persistSleepDisabledSnapshotLocked(true)
	if err == nil {
		t.Fatal("expected persistence failure on read-only dir")
	}

	// The original file content must still be intact
	b, readErr := os.ReadFile(validPath)
	if readErr != nil {
		t.Fatalf("original file should still exist: %v", readErr)
	}
	if string(b) != `{"previous":false}` {
		t.Fatalf("original file was corrupted: %s", string(b))
	}
}

func TestSnapshotPermissionsAre0600(t *testing.T) {
	stubSleepDisabled(t, false)
	path := sleepDisabledPath

	if err := holdSleep("test-reason"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("snapshot file must exist: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("expected mode 0600, got %#o", mode)
	}
}

func TestInitWithoutSnapshotDoesNothing(t *testing.T) {
	fake := stubSleepDisabled(t, false)

	initSleepDisabledState(filepath.Join(t.TempDir(), "absent.json"))

	if fake.writes != 0 {
		t.Fatalf("wrote the setting %d times without a snapshot", fake.writes)
	}
}

// ---------------------------------------------------------------------------
// Adapter policy -- the integration point
// ---------------------------------------------------------------------------

func TestAdapterPolicyHoldsSleepWhenEnabled(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, true)
	conf = &sleepPolicyConf{prevent: true}

	if err := disableAdapterWithSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if adapter.enabled {
		t.Fatal("adapter should be disabled")
	}
	if !sleep.value {
		t.Fatal("sleep should be held while adapter input is cut")
	}

	if err := enableAdapterWithSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if !adapter.enabled {
		t.Fatal("adapter should be enabled again")
	}
	if sleep.value {
		t.Fatal("sleep should be restored once the adapter is back")
	}
}

func TestAdapterPolicyIsInertWhenSettingOff(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, true)
	conf = &sleepPolicyConf{prevent: false}

	if err := disableAdapterWithSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if adapter.enabled {
		t.Fatal("adapter should still be disabled")
	}
	if sleep.writes != 0 {
		t.Fatal("the setting must not be touched when the feature is off")
	}
	if len(sleepHolds) != 0 {
		t.Fatalf("no hold expected, got %v", sleepHolds)
	}
}

func TestAdapterPolicySurvivesRepeatedDisable(t *testing.T) {
	// Mirrors calibration restore, cancel and the /adapter endpoint, which all
	// call disable without checking the current state.
	sleep := stubSleepDisabled(t, false)
	stubAdapter(t, true)
	conf = &sleepPolicyConf{prevent: true}

	for i := 0; i < 4; i++ {
		if err := disableAdapterWithSleepPolicy(); err != nil {
			t.Fatalf("disable #%d: %v", i, err)
		}
	}
	if err := enableAdapterWithSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if sleep.value {
		t.Fatal("one enable must undo repeated disables, or the Mac never sleeps again")
	}
}

func TestAdapterPolicyRollsBackHoldWhenSMCFails(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, true)
	adapter.disableErr = errors.New("SMC write failed")
	conf = &sleepPolicyConf{prevent: true}

	if err := disableAdapterWithSleepPolicy(); err == nil {
		t.Fatal("expected the SMC failure to propagate")
	}
	if sleep.value {
		t.Fatal("sleep must be restored when the adapter could not be disabled")
	}
	if len(sleepHolds) != 0 {
		t.Fatalf("no hold may survive a failed disable, got %v", sleepHolds)
	}
}

func TestAdapterPolicyKeepsEarlierHoldWhenRedundantDisableFails(t *testing.T) {
	// The main loop and calibration disable the adapter on every cycle, also
	// when it is already off. A failed write on such a call leaves the adapter
	// cut, so it must not drop the hold an earlier call took.
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, true)
	conf = &sleepPolicyConf{prevent: true}

	if err := disableAdapterWithSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	adapter.disableErr = errors.New("SMC write failed")
	if err := disableAdapterWithSleepPolicy(); err == nil {
		t.Fatal("expected the SMC failure to propagate")
	}

	if adapter.enabled {
		t.Fatal("the failed write must leave the adapter cut")
	}
	if !sleep.value || !sleepHolds[sleepHoldAdapter] {
		t.Fatalf("the hold of the earlier disable must survive: sleepDisabled=%v holds=%v", sleep.value, sleepHolds)
	}
}

func TestAdapterPolicyAbortsWhenHoldFails(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	sleep.setErr = errors.New("IOPMSetSystemPowerSetting failed")
	adapter := stubAdapter(t, true)
	conf = &sleepPolicyConf{prevent: true}

	if err := disableAdapterWithSleepPolicy(); err == nil {
		t.Fatal("expected an error when the sleep hold could not be taken")
	}
	if !adapter.enabled {
		t.Fatal("adapter input must stay on if Clamshell cannot be protected")
	}
}

func TestAdapterPolicyReleasesHoldAfterSettingSwitchedOff(t *testing.T) {
	// The user may switch the feature off while a hold is active. The enable
	// path must still release it.
	sleep := stubSleepDisabled(t, false)
	stubAdapter(t, true)
	c := &sleepPolicyConf{prevent: true}
	conf = c

	if err := disableAdapterWithSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	c.prevent = false

	if err := enableAdapterWithSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if sleep.value {
		t.Fatal("the hold must be released even after the setting was switched off")
	}
}

func TestAdapterPolicyKeepsHoldWhenEnableFails(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, true)
	conf = &sleepPolicyConf{prevent: true}

	if err := disableAdapterWithSleepPolicy(); err != nil {
		t.Fatal(err)
	}

	adapter.enableErr = errors.New("SMC write failed")
	if err := enableAdapterWithSleepPolicy(); err == nil {
		t.Fatal("expected the SMC failure to propagate")
	}
	if !sleep.value {
		t.Fatal("sleep must stay held while adapter input is still cut")
	}
	if !sleepHolds[sleepHoldAdapter] {
		t.Fatal("the hold must survive a failed enable")
	}
}

func TestReconcileAdapterSleepPolicy_AcquiresHoldWhenAdapterDisabledAndOptionEnabled(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	stubAdapter(t, false) // adapter is disabled
	conf = &sleepPolicyConf{prevent: true}

	if err := reconcileAdapterSleepPolicy(); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if !sleepHolds[sleepHoldAdapter] {
		t.Fatal("expected sleep hold to be acquired")
	}
	if !sleep.value {
		t.Fatal("expected SleepDisabled to be set to true")
	}
}

func TestReconcileAdapterSleepPolicy_ReleasesHoldWhenAdapterEnabled(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, false)
	conf = &sleepPolicyConf{prevent: true}

	if err := reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if !sleepHolds[sleepHoldAdapter] {
		t.Fatal("hold should be active while adapter is disabled")
	}

	// Now adapter becomes enabled
	adapter.enabled = true
	if err := reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if sleepHolds[sleepHoldAdapter] {
		t.Fatal("hold should be released once adapter is enabled")
	}
	if sleep.value {
		t.Fatal("SleepDisabled should be restored to false")
	}
}

func TestReconcileAdapterSleepPolicy_ReleasesHoldWhenOptionDisabled(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	stubAdapter(t, false) // adapter remains disabled
	c := &sleepPolicyConf{prevent: true}
	conf = c

	if err := reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if !sleepHolds[sleepHoldAdapter] {
		t.Fatal("hold should be active while setting is enabled")
	}

	// User disables setting while adapter is still cut
	c.prevent = false
	if err := reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if sleepHolds[sleepHoldAdapter] {
		t.Fatal("hold should be released once setting is disabled")
	}
	if sleep.value {
		t.Fatal("SleepDisabled should be restored to false")
	}
}

func TestReconcileAdapterSleepPolicy_PreExistingSleepDisabled(t *testing.T) {
	sleep := stubSleepDisabled(t, true) // user originally had SleepDisabled=true
	adapter := stubAdapter(t, false)
	conf = &sleepPolicyConf{prevent: true}

	if err := reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if !sleep.value {
		t.Fatal("pre-existing SleepDisabled=true must remain true")
	}

	adapter.enabled = true
	if err := reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if !sleep.value {
		t.Fatal("pre-existing SleepDisabled=true must still remain true after release")
	}
}

func TestReconcileAdapterSleepPolicy_RetriesFailedRelease(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, false)
	conf = &sleepPolicyConf{prevent: true}

	if err := reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}

	adapter.enabled = true
	sleep.setErr = errors.New("IOPMSetSystemPowerSetting failed")

	if err := reconcileAdapterSleepPolicy(); err == nil {
		t.Fatal("expected reconcile to fail when release fails")
	}
	if !sleepHolds[sleepHoldAdapter] {
		t.Fatal("hold must be preserved for retry")
	}

	// Retry succeeds
	sleep.setErr = nil
	if err := reconcileAdapterSleepPolicy(); err != nil {
		t.Fatalf("retry should succeed: %v", err)
	}
	if sleepHolds[sleepHoldAdapter] {
		t.Fatal("hold should now be released")
	}
}

func TestShutdownAdapterAndSleep_AdapterEnableFailurePreservesHolds(t *testing.T) {
	previousCap := capabilities
	t.Cleanup(func() {
		capabilities = previousCap
		sleepHolds = map[string]bool{}
	})

	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, true)
	capabilities = compatibility.Capabilities{AdapterControl: true}

	if err := holdSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}

	adapter.enableErr = errors.New("SMC enable failed")

	if err := shutdownAdapterAndSleep(); err == nil {
		t.Fatal("expected shutdownAdapterAndSleep to return error when adapter enable fails")
	}

	if !sleepHolds[sleepHoldAdapter] {
		t.Fatal("sleep holds must be preserved when adapter enable fails on shutdown")
	}
	if !sleep.value {
		t.Fatal("sleep must remain held when adapter enable fails on shutdown")
	}
}

func TestShutdownAdapterAndSleep_SuccessfulEnableReleasesHolds(t *testing.T) {
	previousCap := capabilities
	t.Cleanup(func() {
		capabilities = previousCap
		sleepHolds = map[string]bool{}
	})

	sleep := stubSleepDisabled(t, false)
	stubAdapter(t, true)
	capabilities = compatibility.Capabilities{AdapterControl: true}

	if err := holdSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}

	if err := shutdownAdapterAndSleep(); err != nil {
		t.Fatalf("shutdownAdapterAndSleep failed: %v", err)
	}

	if len(sleepHolds) != 0 {
		t.Fatalf("sleep holds should be cleared after successful shutdown, got: %v", sleepHolds)
	}
	if sleep.value {
		t.Fatal("sleep setting must be restored after successful shutdown")
	}
}
