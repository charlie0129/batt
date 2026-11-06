package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlie0129/batt/pkg/compatibility"
)

// newTestHolds builds a sleep-hold set over a fake setting, for tests of the
// bookkeeping alone.
func newTestHolds(t *testing.T, initial bool) (*sleepHolds, *fakeSleepSetting) {
	t.Helper()
	fake := &fakeSleepSetting{value: initial}
	h := newSleepHolds(fake)
	h.path = filepath.Join(t.TempDir(), "batt.sleep.json")
	return h, fake
}

// newSleepFixture is a legacy Mac whose SleepDisabled setting starts at
// sleepDisabled and whose adapter starts on or off.
func newSleepFixture(t *testing.T, sleepDisabled, adapterOn, preventSleep bool) *fixture {
	t.Helper()
	f := newFixture(t, firmwareLegacy, withSleepDisabled(sleepDisabled), withAdapter(adapterOn))
	f.mem.SetPreventSleepOnAdapterDisable(preventSleep)
	return f
}

// ---------------------------------------------------------------------------
// Hold bookkeeping
// ---------------------------------------------------------------------------

func TestHoldSleepIsIdempotentPerReason(t *testing.T) {
	// Regression: batt's call sites are not transition-guarded. Several disable
	// adapter input unconditionally, so the same reason arrives repeatedly. A
	// counted hold would never reach zero again.
	h, fake := newTestHolds(t, false)

	for i := 0; i < 5; i++ {
		if err := h.hold(sleepHoldAdapter); err != nil {
			t.Fatalf("hold #%d: %v", i, err)
		}
	}
	if !fake.disabled() {
		t.Fatal("sleep should be disabled")
	}
	if fake.writeCount() != 1 {
		t.Fatalf("setting written %d times, want 1", fake.writeCount())
	}

	if err := h.release(sleepHoldAdapter); err != nil {
		t.Fatalf("release: %v", err)
	}
	if fake.disabled() {
		t.Fatal("a single release must undo repeated holds of the same reason")
	}
	if h.count() != 0 {
		t.Fatalf("holds left over: %v", h.holds)
	}
}

func TestIndependentReasonsEachHold(t *testing.T) {
	h, fake := newTestHolds(t, false)

	if err := h.hold(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if err := h.hold("some-other-reason"); err != nil {
		t.Fatal(err)
	}

	if err := h.release(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if !fake.disabled() {
		t.Fatal("sleep must stay disabled while another reason holds")
	}

	if err := h.release("some-other-reason"); err != nil {
		t.Fatal(err)
	}
	if fake.disabled() {
		t.Fatal("sleep should be restored once the last reason is gone")
	}
}

func TestReleaseSleepHonoursUserSetting(t *testing.T) {
	h, fake := newTestHolds(t, true) // user disabled sleep themselves

	if err := h.hold(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if fake.writeCount() != 0 {
		t.Fatalf("nothing to write when already disabled, got %d writes", fake.writeCount())
	}

	if err := h.release(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if !fake.disabled() {
		t.Fatal("must not re-enable sleep the user had disabled")
	}
}

func TestReleaseWithoutHoldIsNoop(t *testing.T) {
	h, fake := newTestHolds(t, false)

	if err := h.release(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if fake.writeCount() != 0 {
		t.Fatalf("unbalanced release wrote the setting %d times", fake.writeCount())
	}
}

func TestReleaseAllSleepHoldsDropsEveryReason(t *testing.T) {
	h, fake := newTestHolds(t, false)

	for _, reason := range []string{sleepHoldAdapter, "a", "b"} {
		if err := h.hold(reason); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.releaseAll(); err != nil {
		t.Fatal(err)
	}
	if fake.disabled() {
		t.Fatal("releaseAll must restore the setting")
	}
	if h.count() != 0 {
		t.Fatalf("holds left over: %v", h.holds)
	}
}

func TestReleaseAllWithoutHoldsIsNoop(t *testing.T) {
	h, fake := newTestHolds(t, false)

	if err := h.releaseAll(); err != nil {
		t.Fatal(err)
	}
	if fake.writeCount() != 0 {
		t.Fatalf("wrote the setting %d times without holds", fake.writeCount())
	}
}

// ---------------------------------------------------------------------------
// Failure paths
// ---------------------------------------------------------------------------

func TestHoldSleepPropagatesReadFailure(t *testing.T) {
	h, fake := newTestHolds(t, false)
	fake.getErr = errors.New("IOPMCopySystemPowerSettings returned NULL")

	if err := h.hold(sleepHoldAdapter); err == nil {
		t.Fatal("expected the read failure to propagate")
	}
	if h.count() != 0 {
		t.Fatal("no hold may be recorded when the read failed")
	}
	if _, err := os.Stat(h.path); !os.IsNotExist(err) {
		t.Fatal("no snapshot may be left behind when the read failed")
	}
}

func TestHoldSleepCleansUpAfterWriteFailure(t *testing.T) {
	h, fake := newTestHolds(t, false)
	fake.setErr = errors.New("IOPMSetSystemPowerSetting failed")

	if err := h.hold(sleepHoldAdapter); err == nil {
		t.Fatal("expected the write failure to propagate")
	}
	if h.count() != 0 {
		t.Fatal("no hold may be recorded when the write failed")
	}
	if _, err := os.Stat(h.path); !os.IsNotExist(err) {
		t.Fatal("a failed hold must not leave a snapshot behind")
	}
}

func TestFailedReleaseKeepsHoldAndSnapshot(t *testing.T) {
	// If the restore write fails, ownership must not be dropped: otherwise the
	// next hold would snapshot batt's own leftover value as "user intent" and
	// sleep would stay disabled forever.
	h, fake := newTestHolds(t, false)

	if err := h.hold(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}

	restoreValue := false
	fake.setErr = errors.New("IOPMSetSystemPowerSetting failed")
	fake.setErrFor = &restoreValue

	if err := h.release(sleepHoldAdapter); err == nil {
		t.Fatal("expected the restore failure to propagate")
	}
	if !h.holding(sleepHoldAdapter) {
		t.Fatal("the hold must be kept when the restore failed")
	}
	if _, err := os.Stat(h.path); err != nil {
		t.Fatalf("snapshot must survive a failed restore: %v", err)
	}

	// Once the write works again, the retry restores and cleans up.
	fake.setErr = nil
	fake.setErrFor = nil
	if err := h.release(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if fake.disabled() {
		t.Fatal("retry should have restored sleep")
	}
	if _, err := os.Stat(h.path); !os.IsNotExist(err) {
		t.Fatal("snapshot should be gone after a successful retry")
	}
}

func TestFailedSnapshotRemovalPreservesPreExistingSleepSetting(t *testing.T) {
	h, fake := newTestHolds(t, true)
	if err := h.hold(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(h.path, 0o700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(h.path, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.release(sleepHoldAdapter); err == nil {
		t.Fatal("expected snapshot removal to fail")
	}
	if !h.holding(sleepHoldAdapter) || !h.previous || !fake.disabled() {
		t.Fatal("failed removal must preserve the hold and original SleepDisabled=true")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := h.release(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if !fake.disabled() || fake.writeCount() != 0 {
		t.Fatal("retry must not change the user's original SleepDisabled=true")
	}
}

func TestPendingSnapshotWinsOverLiveValue(t *testing.T) {
	// A snapshot on disk means an earlier restore did not complete. The live
	// value is batt's leftover, not the user's setting, and must not be adopted.
	h, fake := newTestHolds(t, true)

	if err := os.WriteFile(h.path, []byte(`{"previous":false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.hold(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if err := h.release(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if fake.disabled() {
		t.Fatal("restore must use the pending snapshot, not the leftover live value")
	}
}

// ---------------------------------------------------------------------------
// Snapshot persistence
// ---------------------------------------------------------------------------

func TestSnapshotIsPersistedAndClearedAgain(t *testing.T) {
	h, _ := newTestHolds(t, false)

	if err := h.hold(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.path); err != nil {
		t.Fatalf("snapshot should exist while sleep is held: %v", err)
	}

	if err := h.release(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.path); !os.IsNotExist(err) {
		t.Fatalf("snapshot should be gone after restore, got %v", err)
	}
}

func TestRecoverSleepDisabledRestoresSnapshotLeftBehind(t *testing.T) {
	fake := &fakeSleepSetting{value: true} // daemon was killed with sleep disabled
	path := filepath.Join(t.TempDir(), "batt.sleep.json")

	if err := os.WriteFile(path, []byte(`{"previous":false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := recoverSleepDisabled(path, fake); err != nil {
		t.Fatal(err)
	}

	if fake.disabled() {
		t.Fatal("recovery must restore the pre-crash value")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot should be consumed, got %v", err)
	}
}

func TestRecoverSleepDisabledKeepsSnapshotWhenRestoreFails(t *testing.T) {
	fake := &fakeSleepSetting{value: true}
	path := filepath.Join(t.TempDir(), "batt.sleep.json")
	fake.setErr = errors.New("IOPMSetSystemPowerSetting failed")

	if err := os.WriteFile(path, []byte(`{"previous":false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := recoverSleepDisabled(path, fake); err == nil {
		t.Fatal("expected recovery to fail")
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot must survive so the next recovery can retry: %v", err)
	}
}

func TestStartupReconcileKeepsSleepDisabledWithoutGap(t *testing.T) {
	// SleepDisabled is still on from the previous run, the adapter is still
	// cut, and the snapshot says the user had sleep enabled.
	f := newSleepFixture(t, true, false, true)
	path := f.sleepPath()

	if err := os.WriteFile(path, []byte(`{"previous":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.d.holds.init(path); err != nil {
		t.Fatal(err)
	}
	if err := f.d.reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}

	if f.smc.adapterEnabled(t) {
		t.Fatal("adapter should remain disabled")
	}
	if !f.sleep.disabled() || !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("startup must retain sleep protection for the disabled adapter")
	}
	for _, value := range f.sleep.writtenValues {
		if !value {
			t.Fatal("startup briefly restored sleep before reacquiring the hold")
		}
	}
}

func TestReloadReconcilesSleepImmediately(t *testing.T) {
	f := newSleepFixture(t, false, false, true)

	if err := f.d.reconcileReloadedSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if f.smc.adapterEnabled(t) || !f.sleep.disabled() || !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("reload must hold sleep before returning while wall power is cut")
	}
}

func TestReloadAppliesTheReloadedConfig(t *testing.T) {
	// SIGHUP re-reads the file and applies what takes effect without a
	// restart: unsupported settings are dropped and the sleep policy follows
	// the new setting.
	f := newFixture(t, firmwareLegacy, withFileConfig(), withAdapter(false))
	f.file.SetPreventSleepOnAdapterDisable(true)
	if err := f.file.Save(); err != nil {
		t.Fatal(err)
	}

	if err := f.d.reload(); err != nil {
		t.Fatal(err)
	}
	if !f.holdsSleep(sleepHoldAdapter) || !f.sleep.disabled() {
		t.Fatal("reload must apply the new sleep policy to the cut adapter")
	}

	f.file.SetPreventSleepOnAdapterDisable(false)
	if err := f.file.Save(); err != nil {
		t.Fatal(err)
	}
	if err := f.d.reload(); err != nil {
		t.Fatal(err)
	}
	if f.holdsSleep(sleepHoldAdapter) || f.sleep.disabled() {
		t.Fatal("reload must release the hold once the setting is off")
	}
}

func TestStartupSleepPolicyFailureRestoresWallPower(t *testing.T) {
	f := newSleepFixture(t, false, false, true)
	f.sleep.setErr = errors.New("IOPM unavailable")

	if err := f.d.ensureStartupSleepPolicy(); err == nil {
		t.Fatal("startup must fail when sleep protection cannot be established")
	}
	if !f.smc.adapterEnabled(t) {
		t.Fatal("startup failure must restore wall power before returning")
	}
}

func TestStartupSleepPolicyFailureKeepsSnapshotWhenAdapterCannotRestore(t *testing.T) {
	f := newSleepFixture(t, false, false, true)
	if err := os.WriteFile(f.sleepPath(), []byte("malformed"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.smc.fail("EnableAdapter", errors.New("SMC unavailable"))

	if err := f.d.ensureStartupSleepPolicy(); err == nil {
		t.Fatal("startup must fail when neither sleep protection nor wall power can be restored")
	}
	if f.smc.adapterEnabled(t) {
		t.Fatal("adapter must still be disabled")
	}
	if _, err := os.Stat(f.sleepPath()); err != nil {
		t.Fatalf("failed recovery must preserve the snapshot: %v", err)
	}
}

// A broken note about the old sleep setting must not keep the daemon from
// starting. The charge limit does not depend on it. The file stays in place.
func TestStartupContinuesWhenSnapshotIsMalformed(t *testing.T) {
	f := newSleepFixture(t, false, true, false)
	if err := os.WriteFile(f.sleepPath(), []byte("{trunc"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := f.d.ensureStartupSleepPolicy(); err != nil {
		t.Fatalf("a malformed snapshot must not stop the daemon: %v", err)
	}
	if b, err := os.ReadFile(f.sleepPath()); err != nil || string(b) != "{trunc" {
		t.Fatalf("the malformed snapshot must stay untouched for manual recovery: %q, %v", b, err)
	}
	if f.sleep.writeCount() != 0 {
		t.Fatalf("nothing may be written for a malformed snapshot, got %d", f.sleep.writeCount())
	}
}

func TestStartupContinuesWhenSnapshotIsMalformedWithoutAdapterControl(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withCapabilities(compatibility.Capabilities{}))
	if err := os.WriteFile(f.sleepPath(), []byte("{trunc"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := f.d.ensureStartupSleepPolicy(); err != nil {
		t.Fatalf("a malformed snapshot must not stop the daemon: %v", err)
	}
	if _, err := os.Stat(f.sleepPath()); err != nil {
		t.Fatalf("the malformed snapshot must be preserved: %v", err)
	}
}

func TestStartupWithMalformedSnapshotRestoresWallPowerAndContinues(t *testing.T) {
	// The adapter is cut and the feature is on, but no hold can be taken while
	// the snapshot is unreadable. Wall power comes back, and the daemon starts.
	f := newSleepFixture(t, false, false, true)
	if err := os.WriteFile(f.sleepPath(), []byte("{trunc"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := f.d.ensureStartupSleepPolicy(); err != nil {
		t.Fatalf("a malformed snapshot must not stop the daemon: %v", err)
	}
	if !f.smc.adapterEnabled(t) {
		t.Fatal("wall power must come back when protection cannot be taken")
	}
	if f.sleep.writeCount() != 0 {
		t.Fatalf("nothing may be written for a malformed snapshot, got %d", f.sleep.writeCount())
	}
	if _, err := os.Stat(f.sleepPath()); err != nil {
		t.Fatalf("the malformed snapshot must be preserved: %v", err)
	}
}

func TestUnusableSnapshotErrorNamesTheFile(t *testing.T) {
	h, _ := newTestHolds(t, false)
	if err := os.WriteFile(h.path, []byte("{trunc"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := h.hold("test-reason")
	if err == nil {
		t.Fatal("hold must fail while the snapshot is malformed")
	}
	if !strings.Contains(err.Error(), h.path) {
		t.Fatalf("the error must name the file so the user can find it: %v", err)
	}
}

func TestInitPreservesMalformedSnapshot(t *testing.T) {
	h, fake := newTestHolds(t, false)

	if err := os.WriteFile(h.path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := h.init(h.path)
	if err == nil {
		t.Fatal("expected error on malformed snapshot")
	}

	if _, statErr := os.Stat(h.path); statErr != nil {
		t.Fatalf("a malformed snapshot must be preserved for recovery, got: %v", statErr)
	}
	if fake.writeCount() != 0 {
		t.Fatalf("nothing should be written for a malformed snapshot, got %d", fake.writeCount())
	}
}

func TestInitPreservesMissingPreviousField(t *testing.T) {
	h, fake := newTestHolds(t, false)

	if err := os.WriteFile(h.path, []byte(`{"other": 123}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := h.init(h.path)
	if err == nil {
		t.Fatal("expected error on missing previous field")
	}

	if _, statErr := os.Stat(h.path); statErr != nil {
		t.Fatalf("snapshot with missing previous must be preserved, got: %v", statErr)
	}
	if fake.writeCount() != 0 {
		t.Fatalf("nothing should be written when previous field is missing, got %d", fake.writeCount())
	}
}

func TestTakeFirstHoldFailsWhenSnapshotMalformed(t *testing.T) {
	h, fake := newTestHolds(t, false)

	if err := os.WriteFile(h.path, []byte(`{"previous": null}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.hold("test-reason"); err == nil {
		t.Fatal("hold must fail when existing snapshot is malformed")
	}

	if fake.writeCount() != 0 {
		t.Fatalf("SetDisabled must not be called when snapshot is malformed, got %d", fake.writeCount())
	}
	if _, statErr := os.Stat(h.path); statErr != nil {
		t.Fatalf("malformed snapshot must be preserved, got: %v", statErr)
	}
}

func TestTakeFirstHoldFailsWhenPersistenceFails(t *testing.T) {
	h, fake := newTestHolds(t, false)
	// Point to a directory that cannot be created or written to
	h.path = filepath.Join(t.TempDir(), "nonexistent", "batt.sleep.json")

	if err := h.hold("test-reason"); err == nil {
		t.Fatal("hold must fail when persistence fails")
	}

	if fake.writeCount() != 0 {
		t.Fatalf("SetDisabled must not be called when persistence fails, got %d", fake.writeCount())
	}
	if h.count() != 0 {
		t.Fatalf("no holds should be recorded on persistence failure, got %v", h.holds)
	}
}

func TestReleaseAllSleepHoldsPreservesHoldsOnRestoreFailure(t *testing.T) {
	h, fake := newTestHolds(t, false)

	if err := h.hold("reason-a"); err != nil {
		t.Fatal(err)
	}
	if err := h.hold("reason-b"); err != nil {
		t.Fatal(err)
	}

	fake.setErr = errors.New("IOPMSetSystemPowerSetting failed")

	if err := h.releaseAll(); err == nil {
		t.Fatal("expected releaseAll to fail when restore fails")
	}

	if h.count() == 0 {
		t.Fatal("releaseAll must retain holds in memory when restore fails")
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

	h := newSleepHolds(&fakeSleepSetting{})
	h.path = validPath
	err := h.persistSnapshotLocked(true)
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
	h, _ := newTestHolds(t, false)

	if err := h.hold("test-reason"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(h.path)
	if err != nil {
		t.Fatalf("snapshot file must exist: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("expected mode 0600, got %#o", mode)
	}
}

func TestInitWithoutSnapshotDoesNothing(t *testing.T) {
	h, fake := newTestHolds(t, false)

	if err := h.init(filepath.Join(t.TempDir(), "absent.json")); err != nil {
		t.Fatal(err)
	}

	if fake.writeCount() != 0 {
		t.Fatalf("wrote the setting %d times without a snapshot", fake.writeCount())
	}
}

// ---------------------------------------------------------------------------
// Adapter policy -- the integration point
// ---------------------------------------------------------------------------

func TestAdapterPolicyHoldsSleepWhenEnabled(t *testing.T) {
	f := newSleepFixture(t, false, true, true)

	if err := f.d.disableAdapter(); err != nil {
		t.Fatal(err)
	}
	if f.smc.adapterEnabled(t) {
		t.Fatal("adapter should be disabled")
	}
	if !f.sleep.disabled() {
		t.Fatal("sleep should be held while adapter input is cut")
	}

	if err := f.d.enableAdapter(); err != nil {
		t.Fatal(err)
	}
	if !f.smc.adapterEnabled(t) {
		t.Fatal("adapter should be enabled again")
	}
	if f.sleep.disabled() {
		t.Fatal("sleep should be restored once the adapter is back")
	}
}

func TestAdapterPolicyIsInertWhenSettingOff(t *testing.T) {
	f := newSleepFixture(t, false, true, false)

	if err := f.d.disableAdapter(); err != nil {
		t.Fatal(err)
	}
	if f.smc.adapterEnabled(t) {
		t.Fatal("adapter should still be disabled")
	}
	if f.sleep.writeCount() != 0 {
		t.Fatal("the setting must not be touched when the feature is off")
	}
	if f.d.holds.count() != 0 {
		t.Fatalf("no hold expected, got %v", f.d.holds.holds)
	}
}

func TestAdapterPolicySurvivesRepeatedDisable(t *testing.T) {
	// Mirrors calibration restore, cancel and the /adapter endpoint, which all
	// call disable without checking the current state.
	f := newSleepFixture(t, false, true, true)

	for i := 0; i < 4; i++ {
		if err := f.d.disableAdapter(); err != nil {
			t.Fatalf("disable #%d: %v", i, err)
		}
	}
	if err := f.d.enableAdapter(); err != nil {
		t.Fatal(err)
	}
	if f.sleep.disabled() {
		t.Fatal("one enable must undo repeated disables, or the Mac never sleeps again")
	}
}

func TestAdapterPolicyRollsBackHoldWhenSMCFails(t *testing.T) {
	f := newSleepFixture(t, false, true, true)
	f.smc.fail("DisableAdapter", errors.New("SMC write failed"))

	if err := f.d.disableAdapter(); err == nil {
		t.Fatal("expected the SMC failure to propagate")
	}
	if f.sleep.disabled() {
		t.Fatal("sleep must be restored when the adapter could not be disabled")
	}
	if f.d.holds.count() != 0 {
		t.Fatalf("no hold may survive a failed disable, got %v", f.d.holds.holds)
	}
}

func TestAdapterPolicyKeepsEarlierHoldWhenRedundantDisableFails(t *testing.T) {
	// The main loop and calibration disable the adapter on every cycle, also
	// when it is already off. A failed write on such a call leaves the adapter
	// cut, so it must not drop the hold an earlier call took.
	f := newSleepFixture(t, false, true, true)

	if err := f.d.disableAdapter(); err != nil {
		t.Fatal(err)
	}
	f.smc.fail("DisableAdapter", errors.New("SMC write failed"))
	if err := f.d.disableAdapter(); err == nil {
		t.Fatal("expected the SMC failure to propagate")
	}

	if f.smc.adapterEnabled(t) {
		t.Fatal("the failed write must leave the adapter cut")
	}
	if !f.sleep.disabled() || !f.holdsSleep(sleepHoldAdapter) {
		t.Fatalf("the hold of the earlier disable must survive: sleepDisabled=%v holds=%v", f.sleep.disabled(), f.d.holds.holds)
	}
}

func TestAdapterPolicyAbortsWhenHoldFails(t *testing.T) {
	f := newSleepFixture(t, false, true, true)
	f.sleep.setErr = errors.New("IOPMSetSystemPowerSetting failed")

	if err := f.d.disableAdapter(); err == nil {
		t.Fatal("expected an error when the sleep hold could not be taken")
	}
	if !f.smc.adapterEnabled(t) {
		t.Fatal("adapter input must stay on if Clamshell cannot be protected")
	}
}

func TestAdapterPolicyReleasesHoldAfterSettingSwitchedOff(t *testing.T) {
	// The user may switch the feature off while a hold is active. The enable
	// path must still release it.
	f := newSleepFixture(t, false, true, true)

	if err := f.d.disableAdapter(); err != nil {
		t.Fatal(err)
	}
	f.mem.SetPreventSleepOnAdapterDisable(false)

	if err := f.d.enableAdapter(); err != nil {
		t.Fatal(err)
	}
	if f.sleep.disabled() {
		t.Fatal("the hold must be released even after the setting was switched off")
	}
}

func TestAdapterPolicyKeepsHoldWhenEnableFails(t *testing.T) {
	f := newSleepFixture(t, false, true, true)

	if err := f.d.disableAdapter(); err != nil {
		t.Fatal(err)
	}

	f.smc.fail("EnableAdapter", errors.New("SMC write failed"))
	if err := f.d.enableAdapter(); err == nil {
		t.Fatal("expected the SMC failure to propagate")
	}
	if !f.sleep.disabled() {
		t.Fatal("sleep must stay held while adapter input is still cut")
	}
	if !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("the hold must survive a failed enable")
	}
}

func TestReconcileAdapterSleepPolicy_AcquiresHoldWhenAdapterDisabledAndOptionEnabled(t *testing.T) {
	f := newSleepFixture(t, false, false, true) // adapter is disabled

	if err := f.d.reconcileAdapterSleepPolicy(); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("expected sleep hold to be acquired")
	}
	if !f.sleep.disabled() {
		t.Fatal("expected SleepDisabled to be set to true")
	}
}

func TestReconcileAdapterSleepPolicy_ReleasesHoldWhenAdapterEnabled(t *testing.T) {
	f := newSleepFixture(t, false, false, true)

	if err := f.d.reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("hold should be active while adapter is disabled")
	}

	// Now adapter becomes enabled
	f.smc.setAdapter(t, true)
	if err := f.d.reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("hold should be released once adapter is enabled")
	}
	if f.sleep.disabled() {
		t.Fatal("SleepDisabled should be restored to false")
	}
}

func TestReconcileAdapterSleepPolicy_ReleasesHoldWhenOptionDisabled(t *testing.T) {
	f := newSleepFixture(t, false, false, true) // adapter remains disabled

	if err := f.d.reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("hold should be active while setting is enabled")
	}

	// User disables setting while adapter is still cut
	f.mem.SetPreventSleepOnAdapterDisable(false)
	if err := f.d.reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("hold should be released once setting is disabled")
	}
	if f.sleep.disabled() {
		t.Fatal("SleepDisabled should be restored to false")
	}
}

func TestReconcileAdapterSleepPolicy_PreExistingSleepDisabled(t *testing.T) {
	f := newSleepFixture(t, true, false, true) // user originally had SleepDisabled=true

	if err := f.d.reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if !f.sleep.disabled() {
		t.Fatal("pre-existing SleepDisabled=true must remain true")
	}

	f.smc.setAdapter(t, true)
	if err := f.d.reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if !f.sleep.disabled() {
		t.Fatal("pre-existing SleepDisabled=true must still remain true after release")
	}
}

func TestReconcileAdapterSleepPolicy_RetriesFailedRelease(t *testing.T) {
	f := newSleepFixture(t, false, false, true)

	if err := f.d.reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}

	f.smc.setAdapter(t, true)
	f.sleep.setErr = errors.New("IOPMSetSystemPowerSetting failed")

	if err := f.d.reconcileAdapterSleepPolicy(); err == nil {
		t.Fatal("expected reconcile to fail when release fails")
	}
	if !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("hold must be preserved for retry")
	}

	// Retry succeeds
	f.sleep.setErr = nil
	if err := f.d.reconcileAdapterSleepPolicy(); err != nil {
		t.Fatalf("retry should succeed: %v", err)
	}
	if f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("hold should now be released")
	}
}

func TestShutdownAdapterAndSleep_AdapterEnableFailurePreservesHolds(t *testing.T) {
	f := newSleepFixture(t, false, true, true)

	if err := f.d.holds.hold(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}

	f.smc.fail("EnableAdapter", errors.New("SMC enable failed"))

	if err := f.d.shutdownAdapterAndSleep(); err == nil {
		t.Fatal("expected shutdownAdapterAndSleep to return error when adapter enable fails")
	}

	if !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("sleep holds must be preserved when adapter enable fails on shutdown")
	}
	if !f.sleep.disabled() {
		t.Fatal("sleep must remain held when adapter enable fails on shutdown")
	}
}

func TestShutdownAdapterAndSleep_SuccessfulEnableReleasesHolds(t *testing.T) {
	f := newSleepFixture(t, false, true, true)

	if err := f.d.holds.hold(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}

	if err := f.d.shutdownAdapterAndSleep(); err != nil {
		t.Fatalf("shutdownAdapterAndSleep failed: %v", err)
	}

	if f.d.holds.count() != 0 {
		t.Fatalf("sleep holds should be cleared after successful shutdown, got: %v", f.d.holds.holds)
	}
	if f.sleep.disabled() {
		t.Fatal("sleep setting must be restored after successful shutdown")
	}
}
