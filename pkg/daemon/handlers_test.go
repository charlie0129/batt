package daemon

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
)

func TestResolveDisableLimit(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name            string
		upper           int
		disableUntil    time.Time
		preDisableLimit int
		want            int
		wantOK          bool
	}{
		{
			name:   "limit is set",
			upper:  80,
			want:   80,
			wantOK: true,
		},
		{
			name:   "already disabled without a timer",
			upper:  100,
			want:   0,
			wantOK: false,
		},
		{
			name:            "already disabled with a pending timer",
			upper:           100,
			disableUntil:    now.Add(time.Hour),
			preDisableLimit: 80,
			want:            80,
			wantOK:          true,
		},
		{
			name:            "saved limit without a timer",
			upper:           100,
			preDisableLimit: 80,
			want:            0,
			wantOK:          false,
		},
		{
			name:            "pending timer with an invalid saved limit",
			upper:           100,
			disableUntil:    now.Add(time.Hour),
			preDisableLimit: 5,
			want:            0,
			wantOK:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newMemConfig()
			c.upper = tt.upper
			c.disableUntil = tt.disableUntil
			c.preDisableLimit = tt.preDisableLimit

			got, gotOK := resolveDisableLimit(c)
			if got != tt.want || gotOK != tt.wantOK {
				t.Errorf("resolveDisableLimit() = (%d, %v), want (%d, %v)", got, gotOK, tt.want, tt.wantOK)
			}
		})
	}
}

func TestSetDisableForRejectsCalibration(t *testing.T) {
	f := newFixture(t, firmwareLegacy)
	f.setCalibration(&calibration.State{Phase: calibration.PhaseCharge})

	response := f.put("/disable", `"1h0m0s"`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if body := response.Body.String(); !strings.Contains(body, "calibration is in progress") {
		t.Fatalf("response does not explain conflict: %s", body)
	}
	if f.mem.UpperLimit() != 80 || !f.mem.DisableUntil().IsZero() {
		t.Fatalf("temporary disable mutated config after rejection: %+v", f.mem.snapshot())
	}
}

func TestSetLimitRejectsCalibration(t *testing.T) {
	for _, phase := range []calibration.Phase{calibration.PhaseCharge, calibration.PhaseError} {
		t.Run(string(phase), func(t *testing.T) {
			f := newFixture(t, firmwareLegacy)
			f.setCalibration(&calibration.State{Phase: phase})

			response := f.put("/limit", "90")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if body := response.Body.String(); !strings.Contains(body, ErrCalibrationControlsChargeLimit.Error()) {
				t.Fatalf("response does not explain conflict: %s", body)
			}
			if f.mem.UpperLimit() != 80 {
				t.Fatalf("upper limit = %d after rejection, want 80", f.mem.UpperLimit())
			}
		})
	}
}

func TestSetAdapterDisableFor(t *testing.T) {
	f := newFixture(t, firmwareLegacy)

	f.mustServe(http.MethodPut, "/adapter/disable", `"1h0m0s"`, http.StatusCreated)
	if f.smc.adapterEnabled(t) {
		t.Fatal("power adapter was not disabled")
	}
	remaining := time.Until(f.mem.AdapterDisableUntil())
	if remaining < 59*time.Minute || remaining > time.Hour {
		t.Fatalf("adapter disable deadline has unexpected remaining duration: %s", remaining)
	}
}

func TestSetAdapterClearsScheduledEnable(t *testing.T) {
	f := newFixture(t, firmwareLegacy)
	f.mem.SetAdapterDisableTimer(time.Now().Add(time.Hour))

	f.mustServe(http.MethodPut, "/adapter", "true", http.StatusCreated)
	if !f.mem.AdapterDisableUntil().IsZero() {
		t.Fatalf("adapter disable timer was not cleared: %s", f.mem.AdapterDisableUntil())
	}
}

func TestSetAdapterDisableForRejectsCalibration(t *testing.T) {
	f := newFixture(t, firmwareLegacy)
	f.setCalibration(&calibration.State{Phase: calibration.PhaseDischarge})

	response := f.put("/adapter/disable", `"1h0m0s"`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if f.smc.callCount("DisableAdapter") != 0 || !f.mem.AdapterDisableUntil().IsZero() {
		t.Fatal("rejected temporary adapter disable changed state")
	}
}

func TestStartCalibrationRequestRejectsTemporaryDisable(t *testing.T) {
	f := newFixture(t, firmwareLegacy)
	f.mem.SetUpperLimit(100)
	f.mem.SetLowerLimit(78)
	f.mem.SetDisableTimer(time.Now().Add(time.Hour), 80)

	response := f.post("/calibration/start")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if body := response.Body.String(); !strings.Contains(body, ErrTemporaryDisableInProgress.Error()) {
		t.Fatalf("response does not explain conflict: %s", body)
	}
	if f.phase() != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", f.phase())
	}
}

func TestSetAdapterMode_RollsBackWhenWallPowerCannotBeRestored(t *testing.T) {
	f := newFixture(t, firmwareGated, withFileConfig(), withCharge(60), withAdapter(false),
		withCapabilities(adapterCaps()))
	f.file.SetAdapterMode(true)
	if err := f.file.Save(); err != nil {
		t.Fatal(err)
	}
	f.smc.fail("EnableAdapter", errors.New("SMC enable failed"))

	response := f.put("/adapter-mode", "false")
	if response.Code != http.StatusInternalServerError || !f.file.AdapterMode() || f.smc.adapterEnabled(t) || f.caps().ChargeControlMode != compatibility.ChargeControlAdapter {
		t.Fatalf("failed transition must keep adapter mode: status=%d, configured=%t, adapter=%t, mode=%s", response.Code, f.file.AdapterMode(), f.smc.adapterEnabled(t), f.caps().ChargeControlMode)
	}
	if reloaded := f.reloadFile(); !reloaded.AdapterMode() {
		t.Fatal("failed transition must roll back persisted config")
	}
}

func TestSetAdapterMode_ReconciliationFailureRollsBackAndRestoresPower(t *testing.T) {
	f := newFixture(t, firmwareGated, withFileConfig(), withCharge(60), withAdapter(false),
		withCapabilities(compatibility.Capabilities{AdapterControl: true}))
	f.file.SetPreventSleepOnAdapterDisable(true)
	if err := f.file.Save(); err != nil {
		t.Fatal(err)
	}
	f.sleep.setErr = errors.New("IOPM unavailable")

	response := f.put("/adapter-mode", "true")
	if response.Code != http.StatusInternalServerError || f.file.AdapterMode() || !f.smc.adapterEnabled(t) {
		t.Fatalf("failed policy must roll back mode and restore wall power: status=%d, configured=%t, adapter=%t", response.Code, f.file.AdapterMode(), f.smc.adapterEnabled(t))
	}
	if reloaded := f.reloadFile(); reloaded.AdapterMode() {
		t.Fatal("failed transition must roll back persisted config")
	}
}

func TestSetPreventSleepOnAdapterDisable_RequiresCapability(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withCapabilities(compatibility.Capabilities{AdapterControl: false}))

	response := f.put("/prevent-sleep-on-adapter-disable", "true")
	if response.Code != http.StatusConflict {
		t.Fatalf("expected status 409 Conflict, got %d", response.Code)
	}
}

func TestSetPreventSleepOnAdapterDisable_EnablingWhileDisabledAcquiresHold(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withAdapter(false))

	f.mustServe(http.MethodPut, "/prevent-sleep-on-adapter-disable", "true", http.StatusCreated)
	if !f.mem.PreventSleepOnAdapterDisable() {
		t.Fatal("configuration was not updated to true")
	}
	if !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("sleep hold was not acquired when enabling while adapter is disabled")
	}
	if !f.sleep.disabled() {
		t.Fatal("SleepDisabled was not set to true")
	}
}

func TestSetPreventSleepOnAdapterDisable_AdapterModeCanEnable(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withAdapter(false), withCapabilities(adapterCaps()))

	response := f.put("/prevent-sleep-on-adapter-disable", "true")
	if response.Code != http.StatusCreated || !f.mem.PreventSleepOnAdapterDisable() || f.smc.adapterEnabled(t) || !f.sleep.disabled() {
		t.Fatalf("adapter mode sleep policy not enabled: status=%d, setting=%t, adapter=%t, sleep=%t", response.Code, f.mem.PreventSleepOnAdapterDisable(), f.smc.adapterEnabled(t), f.sleep.disabled())
	}
}

func TestSetPreventSleepOnAdapterDisable_DisablingWhileDisabledRestoresHold(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withAdapter(false))
	f.mem.SetPreventSleepOnAdapterDisable(true)

	// Acquire initial hold
	if err := f.d.holds.hold(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if !f.sleep.disabled() {
		t.Fatal("SleepDisabled must be true initially")
	}

	f.mustServe(http.MethodPut, "/prevent-sleep-on-adapter-disable", "false", http.StatusCreated)
	if f.mem.PreventSleepOnAdapterDisable() {
		t.Fatal("configuration was not updated to false")
	}
	if f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("sleep hold must be released when setting disabled")
	}
	if f.sleep.disabled() {
		t.Fatal("SleepDisabled must be restored to false")
	}
}

func TestSetPreventSleepOnAdapterDisable_HoldFailurePreventsConfigChange(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withAdapter(false))

	// Simulate hold failure
	f.sleep.setErr = errors.New("IOPMSetSystemPowerSetting error")

	response := f.put("/prevent-sleep-on-adapter-disable", "true")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 error on hold failure, got %d", response.Code)
	}
	if f.mem.PreventSleepOnAdapterDisable() {
		t.Fatal("configuration must NOT be changed when hold acquisition fails")
	}
	if f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("hold must not be recorded")
	}
}

func TestSetPreventSleepOnAdapterDisable_ReleaseFailureReturnsErrorAndKeepsHold(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withAdapter(false))
	f.mem.SetPreventSleepOnAdapterDisable(true)

	if err := f.d.holds.hold(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}

	// Release will fail
	f.sleep.setErr = errors.New("IOPMSetSystemPowerSetting release error")

	response := f.put("/prevent-sleep-on-adapter-disable", "false")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 error on release failure, got %d", response.Code)
	}
	if !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("hold must be preserved for retry when release fails")
	}
}

func TestSetPreventSleepOnAdapterDisable_SerializedWithAdapterTransition(t *testing.T) {
	f := newFixture(t, firmwareLegacy)

	// Acquire transition lock to simulate active adapter transition
	f.d.transitionMu.Lock()

	done := make(chan struct{})
	go func() {
		f.put("/prevent-sleep-on-adapter-disable", "true")
		close(done)
	}()

	select {
	case <-done:
		f.d.transitionMu.Unlock()
		t.Fatal("setting request completed while transitionMu was locked! Must be serialized.")
	case <-time.After(50 * time.Millisecond):
		// Expected: blocked on lock
	}

	f.d.transitionMu.Unlock()
	select {
	case <-done:
		// Succeeded after unlocking
	case <-time.After(500 * time.Millisecond):
		t.Fatal("setting request failed to complete after transitionMu was unlocked")
	}
}
