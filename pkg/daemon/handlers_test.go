package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
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
			c := &mockConf{
				upper:           tt.upper,
				disableUntil:    tt.disableUntil,
				preDisableLimit: tt.preDisableLimit,
			}

			got, gotOK := resolveDisableLimit(c)
			if got != tt.want || gotOK != tt.wantOK {
				t.Errorf("resolveDisableLimit() = (%d, %v), want (%d, %v)", got, gotOK, tt.want, tt.wantOK)
			}
		})
	}
}

func TestSetDisableForRejectsCalibration(t *testing.T) {
	previousConf, previousCapabilities := conf, capabilities
	previousState, previousStatePath := calibrationState, calibrationStatePath
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState, calibrationStatePath = previousState, previousStatePath
	})

	configured := &mockConf{upper: 80, lower: 78}
	conf = configured
	capabilities = compatibility.Capabilities{ChargingControl: true}
	calibrationState = &calibration.State{Phase: calibration.PhaseCharge}
	calibrationStatePath = ""

	request := httptest.NewRequest(http.MethodPut, "/disable", strings.NewReader(`"1h0m0s"`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "calibration is in progress") {
		t.Fatalf("response does not explain conflict: %s", response.Body.String())
	}
	if configured.upper != 80 || !configured.disableUntil.IsZero() {
		t.Fatalf("temporary disable mutated config after rejection: %+v", configured)
	}
}

func TestSetLimitRejectsCalibration(t *testing.T) {
	previousConf, previousCapabilities := conf, capabilities
	previousState, previousStatePath := calibrationState, calibrationStatePath
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState, calibrationStatePath = previousState, previousStatePath
	})

	for _, phase := range []calibration.Phase{calibration.PhaseCharge, calibration.PhaseError} {
		t.Run(string(phase), func(t *testing.T) {
			configured := &mockConf{upper: 80, lower: 78}
			conf = configured
			capabilities = compatibility.Capabilities{ChargingControl: true}
			calibrationState = &calibration.State{Phase: phase}
			calibrationStatePath = ""

			request := httptest.NewRequest(http.MethodPut, "/limit", strings.NewReader("90"))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			setupRoutes().ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), ErrCalibrationControlsChargeLimit.Error()) {
				t.Fatalf("response does not explain conflict: %s", response.Body.String())
			}
			if configured.upper != 80 {
				t.Fatalf("upper limit = %d after rejection, want 80", configured.upper)
			}
		})
	}
}

func TestSetAdapterDisableFor(t *testing.T) {
	previousConf, previousCapabilities := conf, capabilities
	previousState := calibrationState
	previousDisable := smcDisableAdapter
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState = previousState
		smcDisableAdapter = previousDisable
	})

	configured := &mockConf{upper: 80, lower: 78}
	conf = configured
	capabilities = compatibility.Capabilities{AdapterControl: true}
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	disabled := false
	smcDisableAdapter = func() error {
		disabled = true
		return nil
	}

	request := httptest.NewRequest(http.MethodPut, "/adapter/disable", strings.NewReader(`"1h0m0s"`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if !disabled {
		t.Fatal("power adapter was not disabled")
	}
	remaining := time.Until(configured.adapterDisableUntil)
	if remaining < 59*time.Minute || remaining > time.Hour {
		t.Fatalf("adapter disable deadline has unexpected remaining duration: %s", remaining)
	}
}

func TestSetAdapterClearsScheduledEnable(t *testing.T) {
	previousConf, previousCapabilities := conf, capabilities
	previousState := calibrationState
	previousEnable := smcEnableAdapter
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState = previousState
		smcEnableAdapter = previousEnable
	})

	configured := &mockConf{
		upper:               80,
		lower:               78,
		adapterDisableUntil: time.Now().Add(time.Hour),
	}
	conf = configured
	capabilities = compatibility.Capabilities{AdapterControl: true}
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	smcEnableAdapter = func() error { return nil }

	request := httptest.NewRequest(http.MethodPut, "/adapter", strings.NewReader("true"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if !configured.adapterDisableUntil.IsZero() {
		t.Fatalf("adapter disable timer was not cleared: %s", configured.adapterDisableUntil)
	}
}

func TestSetAdapterDisableForRejectsCalibration(t *testing.T) {
	previousConf, previousCapabilities := conf, capabilities
	previousState := calibrationState
	previousDisable := smcDisableAdapter
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState = previousState
		smcDisableAdapter = previousDisable
	})

	configured := &mockConf{upper: 80, lower: 78}
	conf = configured
	capabilities = compatibility.Capabilities{AdapterControl: true}
	calibrationState = &calibration.State{Phase: calibration.PhaseDischarge}
	called := false
	smcDisableAdapter = func() error {
		called = true
		return nil
	}

	request := httptest.NewRequest(http.MethodPut, "/adapter/disable", strings.NewReader(`"1h0m0s"`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if called || !configured.adapterDisableUntil.IsZero() {
		t.Fatal("rejected temporary adapter disable changed state")
	}
}

func TestStartCalibrationRequestRejectsTemporaryDisable(t *testing.T) {
	previousConf, previousCapabilities := conf, capabilities
	previousState, previousStatePath := calibrationState, calibrationStatePath
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState, calibrationStatePath = previousState, previousStatePath
	})

	conf = &mockConf{
		upper:           100,
		lower:           78,
		disableUntil:    time.Now().Add(time.Hour),
		preDisableLimit: 80,
	}
	capabilities = compatibility.Capabilities{Calibration: true}
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	calibrationStatePath = ""

	request := httptest.NewRequest(http.MethodPost, "/calibration/start", nil)
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), ErrTemporaryDisableInProgress.Error()) {
		t.Fatalf("response does not explain conflict: %s", response.Body.String())
	}
	if calibrationState.Phase != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", calibrationState.Phase)
	}
}

type handlerMockConf struct {
	mockConf
	preventSleepOnAdapterDisable bool
}

func (h *handlerMockConf) PreventSleepOnAdapterDisable() bool {
	return h.preventSleepOnAdapterDisable
}

func (h *handlerMockConf) SetPreventSleepOnAdapterDisable(p bool) {
	h.preventSleepOnAdapterDisable = p
}

func TestSetAdapterMode_RollsBackWhenWallPowerCannotBeRestored(t *testing.T) {
	stubSleepDisabled(t, false)
	adapterMockSMC(t, 60, true, false)
	adapter := stubAdapter(t, false)
	adapter.enableErr = errors.New("SMC enable failed")
	file, path := useTempConfig(t)
	file.SetAdapterMode(true)
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}
	previousCap, previousCharger := capabilities, charger
	t.Cleanup(func() { capabilities, charger = previousCap, previousCharger })
	capabilities = compatibility.Capabilities{ChargeControlMode: compatibility.ChargeControlAdapter}

	request := httptest.NewRequest(http.MethodPut, "/adapter-mode", strings.NewReader("false"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError || !file.AdapterMode() || adapter.enabled || capabilities.ChargeControlMode != compatibility.ChargeControlAdapter {
		t.Fatalf("failed transition must keep adapter mode: status=%d, configured=%t, adapter=%t, mode=%s", response.Code, file.AdapterMode(), adapter.enabled, capabilities.ChargeControlMode)
	}
	reloaded, err := config.NewFile(path)
	if err != nil || !reloaded.AdapterMode() {
		t.Fatalf("failed transition must roll back persisted config: config=%v, err=%v", reloaded, err)
	}
}

func TestSetAdapterMode_ReconciliationFailureRollsBackAndRestoresPower(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapterMockSMC(t, 60, true, false)
	adapter := stubAdapter(t, false)
	file, path := useTempConfig(t)
	previousCap, previousCharger := capabilities, charger
	t.Cleanup(func() { capabilities, charger = previousCap, previousCharger })
	capabilities = compatibility.Capabilities{AdapterControl: true}
	file.SetPreventSleepOnAdapterDisable(true)
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}
	sleep.setErr = errors.New("IOPM unavailable")

	request := httptest.NewRequest(http.MethodPut, "/adapter-mode", strings.NewReader("true"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError || file.AdapterMode() || !adapter.enabled {
		t.Fatalf("failed policy must roll back mode and restore wall power: status=%d, configured=%t, adapter=%t", response.Code, file.AdapterMode(), adapter.enabled)
	}
	reloaded, err := config.NewFile(path)
	if err != nil || reloaded.AdapterMode() {
		t.Fatalf("failed transition must roll back persisted config: config=%v, err=%v", reloaded, err)
	}
}

func TestSetPreventSleepOnAdapterDisable_RequiresCapability(t *testing.T) {
	previousConf, previousCap := conf, capabilities
	t.Cleanup(func() { conf, capabilities = previousConf, previousCap })

	conf = &handlerMockConf{}
	capabilities = compatibility.Capabilities{AdapterControl: false}

	request := httptest.NewRequest(http.MethodPut, "/prevent-sleep-on-adapter-disable", strings.NewReader("true"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected status 409 Conflict, got %d", response.Code)
	}
}

func TestSetPreventSleepOnAdapterDisable_EnablingWhileDisabledAcquiresHold(t *testing.T) {
	previousConf, previousCap := conf, capabilities
	previousIsAdapter := smcIsAdapterEnabled
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCap
		smcIsAdapterEnabled = previousIsAdapter
		sleepHolds = map[string]bool{}
	})

	sleep := stubSleepDisabled(t, false)
	capabilities = compatibility.Capabilities{AdapterControl: true}
	smcIsAdapterEnabled = func() (bool, error) { return false, nil } // adapter is disabled
	configured := &handlerMockConf{}
	conf = configured

	request := httptest.NewRequest(http.MethodPut, "/prevent-sleep-on-adapter-disable", strings.NewReader("true"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", response.Code, response.Body.String())
	}
	if !configured.preventSleepOnAdapterDisable {
		t.Fatal("configuration was not updated to true")
	}
	if !sleepHolds[sleepHoldAdapter] {
		t.Fatal("sleep hold was not acquired when enabling while adapter is disabled")
	}
	if !sleep.value {
		t.Fatal("SleepDisabled was not set to true")
	}
}

func TestSetPreventSleepOnAdapterDisable_AdapterModeCanEnable(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, false)
	capabilities = compatibility.Capabilities{ChargeControlMode: compatibility.ChargeControlAdapter}
	configured := &handlerMockConf{}
	conf = configured

	request := httptest.NewRequest(http.MethodPut, "/prevent-sleep-on-adapter-disable", strings.NewReader("true"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusCreated || !configured.preventSleepOnAdapterDisable || adapter.enabled || !sleep.value {
		t.Fatalf("adapter mode sleep policy not enabled: status=%d, setting=%t, adapter=%t, sleep=%t", response.Code, configured.preventSleepOnAdapterDisable, adapter.enabled, sleep.value)
	}
}

func TestSetPreventSleepOnAdapterDisable_DisablingWhileDisabledRestoresHold(t *testing.T) {
	previousConf, previousCap := conf, capabilities
	previousIsAdapter := smcIsAdapterEnabled
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCap
		smcIsAdapterEnabled = previousIsAdapter
		sleepHolds = map[string]bool{}
	})

	sleep := stubSleepDisabled(t, false)
	capabilities = compatibility.Capabilities{AdapterControl: true}
	smcIsAdapterEnabled = func() (bool, error) { return false, nil }
	configured := &handlerMockConf{preventSleepOnAdapterDisable: true}
	conf = configured

	// Acquire initial hold
	if err := holdSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}
	if !sleep.value {
		t.Fatal("SleepDisabled must be true initially")
	}

	request := httptest.NewRequest(http.MethodPut, "/prevent-sleep-on-adapter-disable", strings.NewReader("false"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", response.Code, response.Body.String())
	}
	if configured.preventSleepOnAdapterDisable {
		t.Fatal("configuration was not updated to false")
	}
	if sleepHolds[sleepHoldAdapter] {
		t.Fatal("sleep hold must be released when setting disabled")
	}
	if sleep.value {
		t.Fatal("SleepDisabled must be restored to false")
	}
}

func TestSetPreventSleepOnAdapterDisable_HoldFailurePreventsConfigChange(t *testing.T) {
	previousConf, previousCap := conf, capabilities
	previousIsAdapter := smcIsAdapterEnabled
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCap
		smcIsAdapterEnabled = previousIsAdapter
		sleepHolds = map[string]bool{}
	})

	sleep := stubSleepDisabled(t, false)
	capabilities = compatibility.Capabilities{AdapterControl: true}
	smcIsAdapterEnabled = func() (bool, error) { return false, nil }
	configured := &handlerMockConf{preventSleepOnAdapterDisable: false}
	conf = configured

	// Simulate hold failure
	sleep.setErr = errors.New("IOPMSetSystemPowerSetting error")

	request := httptest.NewRequest(http.MethodPut, "/prevent-sleep-on-adapter-disable", strings.NewReader("true"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 error on hold failure, got %d", response.Code)
	}
	if configured.preventSleepOnAdapterDisable {
		t.Fatal("configuration must NOT be changed when hold acquisition fails")
	}
	if sleepHolds[sleepHoldAdapter] {
		t.Fatal("hold must not be recorded")
	}
}

func TestSetPreventSleepOnAdapterDisable_ReleaseFailureReturnsErrorAndKeepsHold(t *testing.T) {
	previousConf, previousCap := conf, capabilities
	previousIsAdapter := smcIsAdapterEnabled
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCap
		smcIsAdapterEnabled = previousIsAdapter
		sleepHolds = map[string]bool{}
	})

	sleep := stubSleepDisabled(t, false)
	capabilities = compatibility.Capabilities{AdapterControl: true}
	smcIsAdapterEnabled = func() (bool, error) { return false, nil }
	configured := &handlerMockConf{preventSleepOnAdapterDisable: true}
	conf = configured

	if err := holdSleep(sleepHoldAdapter); err != nil {
		t.Fatal(err)
	}

	// Release will fail
	sleep.setErr = errors.New("IOPMSetSystemPowerSetting release error")

	request := httptest.NewRequest(http.MethodPut, "/prevent-sleep-on-adapter-disable", strings.NewReader("false"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 error on release failure, got %d", response.Code)
	}
	if !sleepHolds[sleepHoldAdapter] {
		t.Fatal("hold must be preserved for retry when release fails")
	}
}

func TestSetPreventSleepOnAdapterDisable_SerializedWithAdapterTransition(t *testing.T) {
	previousConf, previousCap := conf, capabilities
	previousIsAdapter := smcIsAdapterEnabled
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCap
		smcIsAdapterEnabled = previousIsAdapter
		sleepHolds = map[string]bool{}
	})

	stubSleepDisabled(t, false)
	capabilities = compatibility.Capabilities{AdapterControl: true}
	smcIsAdapterEnabled = func() (bool, error) { return true, nil }
	configured := &handlerMockConf{}
	conf = configured

	// Acquire transition lock to simulate active adapter transition
	chargeControlTransitionMu.Lock()

	done := make(chan struct{})
	go func() {
		request := httptest.NewRequest(http.MethodPut, "/prevent-sleep-on-adapter-disable", strings.NewReader("true"))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		setupRoutes().ServeHTTP(response, request)
		close(done)
	}()

	select {
	case <-done:
		chargeControlTransitionMu.Unlock()
		t.Fatal("setting request completed while chargeControlTransitionMu was locked! Must be serialized.")
	case <-time.After(50 * time.Millisecond):
		// Expected: blocked on lock
	}

	chargeControlTransitionMu.Unlock()
	select {
	case <-done:
		// Succeeded after unlocking
	case <-time.After(500 * time.Millisecond):
		t.Fatal("setting request failed to complete after chargeControlTransitionMu was unlocked")
	}
}
