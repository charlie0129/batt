package daemon

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
)

// calibrationConf is an in-memory config with the given band.
func calibrationConf(upper, lower int) *memConfig {
	c := newMemConfig()
	c.upper, c.lower = upper, lower
	return c
}

func TestStartCalibrationRejectsTemporaryDisable(t *testing.T) {
	conf := calibrationConf(100, 78)
	conf.SetDisableTimer(time.Now().Add(time.Hour), 80)
	f := newFixture(t, firmwareLegacy, withConfig(conf))

	if err := f.d.startCalibration(15, 10); err != ErrTemporaryDisableInProgress {
		t.Fatalf("startCalibration() error = %v, want %v", err, ErrTemporaryDisableInProgress)
	}
	if f.phase() != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", f.phase())
	}
	if f.asserts.preventCalibration != 0 {
		t.Fatal("rejected calibration acquired a sleep assertion")
	}
}

func TestStartCalibrationRejectsTemporaryAdapterDisable(t *testing.T) {
	conf := calibrationConf(80, 78)
	conf.SetAdapterDisableTimer(time.Now().Add(time.Hour))
	f := newFixture(t, firmwareLegacy, withConfig(conf))

	if err := f.d.startCalibration(15, 10); err != ErrTemporaryAdapterDisableInProgress {
		t.Fatalf("startCalibration() error = %v, want %v", err, ErrTemporaryAdapterDisableInProgress)
	}
	if f.asserts.preventCalibration != 0 {
		t.Fatal("rejected calibration acquired a sleep assertion")
	}
}

// TestCalibrationFlow simulates the main phase transitions on a legacy Mac.
func TestCalibrationFlow(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withConfig(calibrationConf(80, 78)),
		withCharge(40), withCharging(false), withAdapter(true))

	if err := f.d.startCalibration(15, 1); err != nil {
		t.Fatalf("startCalibration failed: %v", err)
	}
	if f.phase() != calibration.PhaseDischarge {
		t.Fatalf("expected discharge phase, got %s", f.phase())
	}
	if !f.asserts.holdsCalibrationSleep() {
		t.Fatal("a running calibration must hold the calibration sleep assertion")
	}
	if err := f.d.pauseCalibration(); err != nil {
		t.Fatal(err)
	}
	if f.asserts.allowCalibration != 0 {
		t.Fatal("pausing calibration released its sleep assertion")
	}
	if err := f.d.resumeCalibration(); err != nil {
		t.Fatal(err)
	}

	// Move charge below threshold to trigger charging phase
	f.d.applyCalibrationWithinLoop(14)
	if f.phase() != calibration.PhaseCharge {
		t.Fatalf("expected charge phase, got %s", f.phase())
	}
	if !f.smc.chargingEnabled(t) {
		t.Fatalf("expected charging enabled")
	}
	// In current implementation, we enable adapter to allow charging to full
	if !f.smc.adapterEnabled(t) {
		t.Fatalf("expected adapter enabled")
	}

	// Simulate reaching full
	f.d.applyCalibrationWithinLoop(100)
	if f.phase() != calibration.PhaseHold {
		t.Fatalf("expected hold phase, got %s", f.phase())
	}
	if f.calibration().HoldEndTime.IsZero() {
		t.Fatalf("HoldEndTime should be set")
	}

	// Fast-forward hold period
	f.calibration().HoldEndTime = time.Now().Add(-time.Second)
	f.d.applyCalibrationWithinLoop(100)
	if f.phase() != calibration.PhasePostHold {
		t.Fatalf("expected post-hold discharge phase, got %s", f.phase())
	}

	// Simulate discharging back down to snapshot upper limit (original upper 80)
	f.d.applyCalibrationWithinLoop(80)
	if f.phase() != calibration.PhaseRestore {
		t.Fatalf("expected restore phase after post-hold discharge, got %s", f.phase())
	}

	// Perform restore
	f.d.applyCalibrationWithinLoop(80)
	if f.phase() != calibration.PhaseIdle {
		t.Fatalf("expected idle at end, got %s", f.phase())
	}
	if f.asserts.preventCalibration != 1 || f.asserts.allowCalibration != 1 || f.asserts.holdsCalibrationSleep() {
		t.Fatalf("sleep assertion calls = prevent:%d allow:%d, want 1/1 and released", f.asserts.preventCalibration, f.asserts.allowCalibration)
	}
}

func TestCalibrationFlowWithFirmwareChargeControl(t *testing.T) {
	f := newFixture(t, firmwareManaged, withConfig(calibrationConf(80, 78)), withFirmwareLimit(true, 78, 80))

	if err := f.d.startCalibration(15, 10); err != nil {
		t.Fatal(err)
	}
	f.d.applyCalibrationWithinLoop(40)
	if f.smc.adapterEnabled(t) {
		t.Fatal("adapter should be disabled during discharge")
	}

	f.d.applyCalibrationWithinLoop(14)
	if f.phase() != calibration.PhaseCharge {
		t.Fatalf("phase = %s, want charge", f.phase())
	}
	if f.smc.firmwareLimit(t).Active {
		t.Fatal("firmware charge limit should be inactive while charging to full")
	}

	f.d.applyCalibrationWithinLoop(100)
	f.calibration().HoldEndTime = time.Now().Add(-time.Second)
	f.d.applyCalibrationWithinLoop(100)
	f.d.applyCalibrationWithinLoop(80)
	f.d.applyCalibrationWithinLoop(80)
	if f.phase() != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", f.phase())
	}
	if state := f.smc.firmwareLimit(t); !state.Active || state.Lower != 78 || state.Upper != 80 {
		t.Fatalf("firmware charge limit was not restored: %+v", state)
	}
	if f.asserts.preventCalibration != 1 || f.asserts.allowCalibration != 1 {
		t.Fatalf("sleep assertion calls = prevent:%d allow:%d, want 1/1", f.asserts.preventCalibration, f.asserts.allowCalibration)
	}
}

func TestStartCalibration_WithAdapterDisabledAndOptionEnabledAcquiresHold(t *testing.T) {
	conf := calibrationConf(80, 78)
	conf.SetPreventSleepOnAdapterDisable(true)
	f := newFixture(t, firmwareLegacy, withConfig(conf), withAdapter(false), withCharging(true))

	if err := f.d.startCalibration(15, 60); err != nil {
		t.Fatalf("startCalibration failed: %v", err)
	}

	if !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("expected sleep hold to be acquired when calibration starts with disabled adapter")
	}
	if !f.sleep.disabled() {
		t.Fatal("expected SleepDisabled to be true")
	}
}

func TestStartCalibration_AdapterReadErrorAborts(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withConfig(calibrationConf(80, 78)))
	f.smc.fail("IsAdapterEnabled", errors.New("SMC read error"))

	if err := f.d.startCalibration(15, 60); err == nil {
		t.Fatal("expected startCalibration to fail on adapter read error")
	}
	if f.phase() != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", f.phase())
	}
	if f.asserts.preventCalibration != 0 {
		t.Fatalf("calibration sleep assertion acquired before preflight completed: %d", f.asserts.preventCalibration)
	}
}

func TestCalibrationDischarge_InvokesDisableWrapperUnconditionally(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withConfig(calibrationConf(80, 78)), withAdapter(false))
	f.setCalibration(&calibration.State{
		Phase:              calibration.PhaseDischarge,
		Threshold:          15,
		SnapshotAdapterOn:  false,
		SnapshotChargingOn: false,
	})

	f.d.applyCalibrationWithinLoop(50) // discharge in progress
	if f.smc.callCount("DisableAdapter") == 0 {
		t.Fatal("expected the adapter to be disabled unconditionally in discharge phase")
	}
}

func TestCalibrationRestore_AdapterEnableFailurePreservesRecoverableState(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withConfig(calibrationConf(80, 78)), withCharge(80))
	f.smc.fail("EnableAdapter", errors.New("SMC enable failed"))
	f.setCalibration(&calibration.State{
		Phase:              calibration.PhaseRestore,
		SnapshotUpperLimit: 80,
		SnapshotLowerLimit: 78,
		SnapshotAdapterOn:  true,
	})

	f.d.applyCalibrationWithinLoop(80)
	if f.phase() != calibration.PhaseError {
		t.Fatalf("phase = %s, want error", f.phase())
	}
	if !strings.Contains(f.calibration().LastError, "SMC enable failed") {
		t.Fatalf("unexpected LastError: %s", f.calibration().LastError)
	}
}

func TestCancelCalibration_RetriesFailedAdapterRestoration(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withConfig(calibrationConf(80, 78)), withCharge(80))
	f.smc.fail("EnableAdapter", errors.New("SMC enable failed"))
	f.setCalibration(&calibration.State{
		Phase:              calibration.PhaseDischarge,
		SnapshotUpperLimit: 80,
		SnapshotLowerLimit: 78,
		SnapshotAdapterOn:  true,
	})

	if err := f.d.cancelCalibration(); err == nil {
		t.Fatal("expected cancelCalibration to fail when adapter enable fails")
	}
	if f.phase() != calibration.PhaseError {
		t.Fatalf("phase = %s, want error", f.phase())
	}

	// Retry succeeds
	f.smc.fail("EnableAdapter", nil)
	if err := f.d.cancelCalibration(); err != nil {
		t.Fatalf("cancelCalibration should succeed on retry: %v", err)
	}
	if f.phase() != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", f.phase())
	}
}

func TestCalibrationHoldToPostHold_DisableFailureIsAnError(t *testing.T) {
	// The post-hold phase never cuts the adapter again. If the cut fails at the
	// transition, the calibration would wait at full charge without a message.
	f := newFixture(t, firmwareLegacy, withConfig(calibrationConf(80, 78)), withCharge(100))
	f.smc.fail("DisableAdapter", errors.New("sleep hold could not be taken"))
	f.setCalibration(&calibration.State{
		Phase:              calibration.PhaseHold,
		HoldEndTime:        time.Now().Add(-time.Minute),
		SnapshotUpperLimit: 80,
		SnapshotLowerLimit: 78,
		SnapshotAdapterOn:  true,
	})

	f.d.applyCalibrationWithinLoop(100)

	if f.phase() != calibration.PhaseError {
		t.Fatalf("phase = %s, want %s", f.phase(), calibration.PhaseError)
	}
	if f.calibration().LastError == "" {
		t.Fatal("the failed adapter cut must be reported in LastError")
	}
}

func TestStartCalibration_RejectsFailedCalibrationUntilCancelled(t *testing.T) {
	// A failed calibration may leave the charge limit at 100. Its snapshot is the
	// only record of the original limit, so a new start must not replace it.
	f := newFixture(t, firmwareLegacy, withConfig(calibrationConf(100, 98)))
	f.setCalibration(&calibration.State{
		Phase:              calibration.PhaseError,
		SnapshotUpperLimit: 80,
		SnapshotLowerLimit: 78,
		SnapshotMaintain:   true,
	})

	err := f.d.startCalibration(15, 60)

	if err != ErrCalibrationControlsChargeLimit {
		t.Fatalf("startCalibration() = %v, want %v", err, ErrCalibrationControlsChargeLimit)
	}
	if st := f.calibration(); st.Phase != calibration.PhaseError || st.SnapshotUpperLimit != 80 {
		t.Fatalf("the snapshot of the failed calibration must stay: phase=%s snapshotUpper=%d", st.Phase, st.SnapshotUpperLimit)
	}
}

func TestCalibrationHoldEnd_DisableFailureRestoresTheChargeLimit(t *testing.T) {
	// The calibration sets the limit to 100 when the charge phase starts. A
	// failed calibration waits for Cancel, but the maintain loop must not find
	// the battery without a limit meanwhile: at limit 100 the legacy loop drops
	// a calibration in the error phase without restoring anything.
	conf := calibrationConf(100, 98)
	f := newFixture(t, firmwareLegacy, withConfig(conf), withCharge(100))
	f.smc.fail("DisableAdapter", errors.New("sleep hold could not be taken"))
	f.setCalibration(&calibration.State{
		Phase:              calibration.PhaseHold,
		HoldEndTime:        time.Now().Add(-time.Minute),
		SnapshotUpperLimit: 80,
		SnapshotLowerLimit: 78,
		SnapshotMaintain:   true,
		SnapshotAdapterOn:  true,
	})

	f.d.applyCalibrationWithinLoop(100)

	if f.phase() != calibration.PhaseError {
		t.Fatalf("phase = %s, want %s", f.phase(), calibration.PhaseError)
	}
	if conf.UpperLimit() != 80 || conf.LowerLimit() != 78 {
		t.Fatalf("limits = %d/%d, want the snapshot 80/78 back", conf.UpperLimit(), conf.LowerLimit())
	}
	if f.calibration().SnapshotUpperLimit != 80 {
		t.Fatalf("the snapshot must stay until Cancel: snapshotUpper=%d", f.calibration().SnapshotUpperLimit)
	}
}

func TestCalibrationStateIsPersistedAndRestoredPaused(t *testing.T) {
	// A calibration that was mid-flow when the daemon stopped comes back
	// paused, so it cannot cut power before the user has a look.
	conf := calibrationConf(80, 78)
	f := newFixture(t, firmwareLegacy, withConfig(conf), withCharge(40), withCharging(false))
	if err := f.d.startCalibration(15, 10); err != nil {
		t.Fatal(err)
	}

	restarted := newFixture(t, firmwareLegacy, withConfig(conf), withCharge(40), withCharging(false))
	restarted.d.loadCalibrationState(f.d.calibrationStatePath)
	st := restarted.calibration()
	if st.Phase != calibration.PhaseDischarge || !st.Paused {
		t.Fatalf("restarted calibration = %+v, want discharge phase, paused", st)
	}
	restarted.d.restoreCalibrationSleepAssertion()
	if !restarted.asserts.holdsCalibrationSleep() {
		t.Fatal("a restored calibration must take its sleep assertion again")
	}
}

func TestUnsupportedModeDiscardsPersistedCalibration(t *testing.T) {
	// After a firmware upgrade that removed adapter control, a persisted
	// calibration cannot continue. Its saved limits are restored and the
	// workflow is dropped.
	conf := calibrationConf(100, 98)
	f := newFixture(t, firmwareGatedNoAdapter, withConfig(conf), withNative(sliderNative(80, 100)))
	if f.caps().Calibration {
		t.Fatalf("precondition: calibration must be unsupported, got %+v", f.caps())
	}
	f.setCalibration(&calibration.State{
		Phase:              calibration.PhaseCharge,
		SnapshotUpperLimit: 80,
		SnapshotLowerLimit: 78,
	})
	if err := f.asserts.PreventCalibrationSleep(); err != nil {
		t.Fatal(err)
	}

	f.d.disableUnsupportedCalibrationState()

	if f.phase() != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", f.phase())
	}
	if conf.UpperLimit() != 80 || conf.LowerLimit() != 78 {
		t.Fatalf("limits = %d/%d, want the snapshot 80/78", conf.UpperLimit(), conf.LowerLimit())
	}
	if f.asserts.holdsCalibrationSleep() {
		t.Fatal("discarding the calibration must release its sleep assertion")
	}
}

func TestScheduledCalibrationPreCheck(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withConfig(calibrationConf(80, 78)), withPluggedIn(false))
	if err := f.d.scheduledCalibrationPreCheck(); err == nil {
		t.Fatal("a scheduled calibration must not start on battery")
	}
	f.smc.setPluggedIn(t, true)
	if err := f.d.scheduledCalibrationPreCheck(); err != nil {
		t.Fatalf("plugged in: %v", err)
	}
	f.setCalibration(&calibration.State{Phase: calibration.PhaseCharge})
	if err := f.d.scheduledCalibrationPreCheck(); !errors.Is(err, ErrCalibrationInProgress) {
		t.Fatalf("running calibration: %v, want %v", err, ErrCalibrationInProgress)
	}
	_ = compatibility.FeatureCalibration
}
