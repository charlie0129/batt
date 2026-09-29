package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charlie0129/gosmc"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
	"github.com/charlie0129/batt/pkg/utils/ptr"
)

// stubBatteryCharge replaces the battery-charge test seam for one test.
func stubBatteryCharge(t *testing.T, charge int) {
	t.Helper()
	previous := smcGetBatteryCharge
	t.Cleanup(func() { smcGetBatteryCharge = previous })
	smcGetBatteryCharge = func() (int, error) { return charge, nil }
}

// useChargeOnceDaemonState installs daemon globals a charge-control request
// needs and restores them afterwards.
func useChargeOnceDaemonState(t *testing.T, configured *mockConf, phase calibration.Phase) {
	t.Helper()
	previousConf, previousCapabilities := conf, capabilities
	previousState, previousStatePath := calibrationState, calibrationStatePath
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState, calibrationStatePath = previousState, previousStatePath
	})

	conf = configured
	capabilities = compatibility.Capabilities{ChargingControl: true}
	calibrationState = &calibration.State{Phase: phase}
	calibrationStatePath = ""
}

// The one-time charge routes, named so the tests below stay readable.
const (
	chargeOnceLimitPath  = "/charge-once/limit"
	chargeOnceFullPath   = "/charge-once/full"
	chargeOnceCancelPath = "/charge-once/cancel"
)

func postChargeOnce(path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, nil)
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)
	return response
}

func TestResolveChargeOnceTarget(t *testing.T) {
	tests := []struct {
		name    string
		upper   int
		full    bool
		want    int
		wantErr error
	}{
		{name: "to the configured limit", upper: 70, want: 70},
		{name: "to full", upper: 70, full: true, want: 100},
		{name: "limit disabled", upper: 100, wantErr: ErrChargeLimitDisabled},
		{name: "limit disabled, full requested", upper: 100, full: true, wantErr: ErrChargeLimitDisabled},
		// A hand-edited config can hold a limit batt never writes.
		{name: "limit below the supported range", upper: 5, wantErr: ErrChargeLimitTooLow},
		{name: "limit below the supported range, full requested", upper: 5, full: true, wantErr: ErrChargeLimitTooLow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveChargeOnceTarget(&mockConf{upper: tt.upper, lower: tt.upper - 2}, tt.full)
			if err != tt.wantErr {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("target = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestChargeOnceStartedMessage(t *testing.T) {
	if got := chargeOnceStartedMessage(70, 58, 70); !strings.Contains(got, "70%") || !strings.Contains(got, "58%") {
		t.Fatalf("message does not name target and charge: %q", got)
	}
	full := chargeOnceStartedMessage(100, 58, 70)
	if !strings.Contains(full, "100%") || !strings.Contains(full, "70%") {
		t.Fatalf("full message does not name target and restored limit: %q", full)
	}
}

func useChargeOnceLegacyAdmissionSMC(t *testing.T, charge int) {
	t.Helper()
	previousSMC, previousCharger := smcConn, charger
	t.Cleanup(func() { smcConn, charger = previousSMC, previousCharger })
	smcConn = legacyChargeMock(t, charge, false)
	charger = chargeKeySwitch{}
	capabilities.ChargeControlMode = compatibility.ChargeControlLegacy
}

func TestStartChargeOnceToLimitInsideHysteresisGap(t *testing.T) {
	// 58% with a persistent 40-70% band: the charge sits in the gap where batt
	// normally waits, so this is the case the feature exists for.
	configured := &mockConf{upper: 70, lower: 40}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	stubBatteryCharge(t, 58)
	useChargeOnceLegacyAdmissionSMC(t, 58)

	response := postChargeOnce(chargeOnceLimitPath)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if configured.chargeOnceTarget != 70 {
		t.Fatalf("chargeOnceTarget = %d, want 70", configured.chargeOnceTarget)
	}
	if configured.upper != 70 || configured.lower != 40 {
		t.Fatalf("configured band changed to %d/%d, want 70/40", configured.upper, configured.lower)
	}
}

func TestStartChargeOnceToFullKeepsConfiguredLimit(t *testing.T) {
	configured := &mockConf{upper: 70, lower: 68}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	stubBatteryCharge(t, 70)
	useChargeOnceLegacyAdmissionSMC(t, 70)

	response := postChargeOnce(chargeOnceFullPath)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if configured.chargeOnceTarget != 100 {
		t.Fatalf("chargeOnceTarget = %d, want 100", configured.chargeOnceTarget)
	}
	// Unlike "batt disable --for", a one-time charge never writes limit=100, so
	// there is no saved limit that a crash could strand.
	if configured.upper != 70 || !configured.disableUntil.IsZero() {
		t.Fatalf("configured limit changed: %+v", configured)
	}
}

func TestStartChargeOnceRejections(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		conf    mockConf
		phase   calibration.Phase
		charge  int
		wantErr string
	}{
		{
			name:    "already at the target",
			path:    chargeOnceLimitPath,
			conf:    mockConf{upper: 70, lower: 40},
			charge:  70,
			wantErr: "already at 70%",
		},
		{
			name:    "already full",
			path:    chargeOnceFullPath,
			conf:    mockConf{upper: 70, lower: 68},
			charge:  100,
			wantErr: "already at 100%",
		},
		{
			name:    "charge limit disabled",
			path:    chargeOnceFullPath,
			conf:    mockConf{upper: 100, lower: 98},
			charge:  50,
			wantErr: "batt is not limiting charging",
		},
		{
			name:    "calibration owns the charge limit",
			path:    chargeOnceLimitPath,
			conf:    mockConf{upper: 70, lower: 40},
			phase:   calibration.PhaseCharge,
			charge:  58,
			wantErr: ErrCalibrationControlsChargeLimit.Error(),
		},
		{
			name:    "temporary disable is pending",
			path:    chargeOnceFullPath,
			conf:    mockConf{upper: 100, lower: 68, disableUntil: time.Now().Add(time.Hour), preDisableLimit: 70},
			charge:  58,
			wantErr: ErrTemporaryDisableInProgress.Error(),
		},
		{
			name:    "one-time charge already running",
			path:    chargeOnceFullPath,
			conf:    mockConf{upper: 70, lower: 68, chargeOnceTarget: 70},
			charge:  58,
			wantErr: ErrChargeOnceInProgress.Error(),
		},
		{
			// Force discharge cuts the power the one-time charge needs, so it
			// would sit there without making progress.
			name:    "temporary adapter disable is pending",
			path:    chargeOnceLimitPath,
			conf:    mockConf{upper: 70, lower: 40, adapterDisableUntil: time.Now().Add(time.Hour)},
			charge:  58,
			wantErr: ErrTemporaryAdapterDisableInProgress.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := tt.phase
			if phase == "" {
				phase = calibration.PhaseIdle
			}
			configured := tt.conf
			before := configured
			useChargeOnceDaemonState(t, &configured, phase)
			stubBatteryCharge(t, tt.charge)

			response := postChargeOnce(tt.path)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), tt.wantErr) {
				t.Fatalf("response does not explain the rejection: %s", response.Body.String())
			}
			if configured != before {
				t.Fatalf("rejected request mutated config: %+v, want %+v", configured, before)
			}
		})
	}
}

func TestCancelChargeOnceRestoresConfiguredBehavior(t *testing.T) {
	configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 100}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)

	response := postChargeOnce(chargeOnceCancelPath)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if configured.chargeOnceTarget != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0", configured.chargeOnceTarget)
	}
	if configured.upper != 70 || configured.lower != 40 {
		t.Fatalf("configured band changed to %d/%d, want 70/40", configured.upper, configured.lower)
	}
}

func TestCancelChargeOnceWithoutSession(t *testing.T) {
	useChargeOnceDaemonState(t, &mockConf{upper: 70, lower: 40}, calibration.PhaseIdle)

	response := postChargeOnce(chargeOnceCancelPath)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), ErrChargeOnceNotRunning.Error()) {
		t.Fatalf("response does not explain the rejection: %s", response.Body.String())
	}
}

func TestSetLimitCancelsChargeOnce(t *testing.T) {
	configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 100}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	stubBatteryCharge(t, 58)
	previousSMC := smcConn
	t.Cleanup(func() { smcConn = previousSMC })
	smcConn = smc.NewMockValues()

	request := httptest.NewRequest(http.MethodPut, "/limit", strings.NewReader("60"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if configured.chargeOnceTarget != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0 after an explicit limit change", configured.chargeOnceTarget)
	}
	if configured.upper != 60 {
		t.Fatalf("upper = %d, want 60", configured.upper)
	}
}

func TestSupersedeChargeOnceSaveFailureKeepsTargetAndLimit(t *testing.T) {
	for _, tt := range []struct {
		name, path, body string
	}{
		{"limit change", "/limit", "60"},
		{"timed disable", "/disable", `"1h"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 100, saveErr: errors.New("disk full")}
			useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
			request := httptest.NewRequest(http.MethodPut, tt.path, strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			setupRoutes().ServeHTTP(response, request)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500: %s", response.Code, response.Body.String())
			}
			if configured.chargeOnceTarget != 100 || configured.upper != 70 || !configured.disableUntil.IsZero() {
				t.Fatalf("failed save must keep original target and limit: %+v", configured)
			}
		})
	}
}

func TestStartCalibrationRejectsChargeOnce(t *testing.T) {
	configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 100}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	capabilities = compatibility.Capabilities{ChargingControl: true, Calibration: true}
	stubCalibrationSleep(t)

	response := postChargeOnce("/calibration/start")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), ErrChargeOnceInProgress.Error()) {
		t.Fatalf("response does not explain the conflict: %s", response.Body.String())
	}
	if calibrationState.Phase != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", calibrationState.Phase)
	}
}

func TestCompleteChargeOnce(t *testing.T) {
	tests := []struct {
		name     string
		target   int
		charge   int
		phase    calibration.Phase
		want     bool
		wantLeft int
	}{
		{name: "no one-time charge", charge: 58, want: false},
		{name: "below the target", target: 70, charge: 58, want: false, wantLeft: 70},
		{name: "reached the target", target: 70, charge: 70, want: true},
		{name: "overshot the target", target: 100, charge: 100, want: true},
		{
			// A persisted conflict can be loaded after a restart. Calibration
			// writes the charge limit itself, so it goes first.
			name:     "calibration owns the charge limit",
			target:   70,
			charge:   70,
			phase:    calibration.PhaseCharge,
			want:     false,
			wantLeft: 70,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := tt.phase
			if phase == "" {
				phase = calibration.PhaseIdle
			}
			previousState, previousCapabilities := calibrationState, capabilities
			t.Cleanup(func() { calibrationState, capabilities = previousState, previousCapabilities })
			calibrationState = &calibration.State{Phase: phase}
			// The completion charge depends on the backend, so name it.
			capabilities = compatibility.Capabilities{
				ChargingControl:   true,
				ChargeControlMode: compatibility.ChargeControlLegacy,
			}
			stubBatteryCharge(t, tt.charge)

			configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: tt.target}
			if got := completeChargeOnce(configured); got != tt.want {
				t.Fatalf("completeChargeOnce() = %v, want %v", got, tt.want)
			}
			if configured.chargeOnceTarget != tt.wantLeft {
				t.Fatalf("chargeOnceTarget = %d, want %d", configured.chargeOnceTarget, tt.wantLeft)
			}
			if configured.upper != 70 || configured.lower != 40 {
				t.Fatalf("configured band changed to %d/%d, want 70/40", configured.upper, configured.lower)
			}
		})
	}
}

// legacyChargeMock builds an SMC mock for the direct charge-control backend.
func legacyChargeMock(t *testing.T, charge int, chargingEnabled bool) *smc.AppleSMC {
	t.Helper()
	value := func(key string, dataType gosmc.DataType, data ...byte) gosmc.Value {
		v, err := gosmc.NewValue(key, dataType, data)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	chargingByte := byte(0x02)
	if chargingEnabled {
		chargingByte = 0x00
	}
	mock := smc.NewMockValues(
		value(smc.ChargingKey1, gosmc.TypeUInt8, chargingByte),
		value(smc.ChargingKey2, gosmc.TypeUInt8, chargingByte),
		value(smc.BatteryChargeKey, gosmc.TypeUInt8, byte(charge)),
		value(smc.ACPowerKey, gosmc.TypeUInt8, 1),
	)
	if err := mock.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mock.Close() })
	return mock
}

func useLegacyLoopState(t *testing.T, mock *smc.AppleSMC, configured *mockConf) {
	t.Helper()
	previousSMC, previousConf, previousCapabilities := smcConn, conf, capabilities
	previousState := calibrationState
	t.Cleanup(func() {
		smcConn, conf, capabilities = previousSMC, previousConf, previousCapabilities
		calibrationState = previousState
	})
	smcConn = mock
	conf = configured
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	capabilities = compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlLegacy,
	}
}

func TestLegacyMaintainLoopRespectsChargeOnceTarget(t *testing.T) {
	tests := []struct {
		name         string
		charge       int
		target       int
		charging     bool
		wantCharging bool
	}{
		{
			// Without a one-time charge, 58% inside a 40-70% band keeps its
			// state: this is the hysteresis gap.
			name: "hysteresis gap without a one-time charge", charge: 58, charging: false, wantCharging: false,
		},
		{
			name: "one-time charge starts charging inside the gap", charge: 58, target: 70, charging: false, wantCharging: true,
		},
		{
			name: "one-time charge stops at its target", charge: 70, target: 70, charging: true, wantCharging: false,
		},
		{
			name: "one-time charge to full keeps charging above the limit", charge: 80, target: 100, charging: true, wantCharging: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := legacyChargeMock(t, tt.charge, tt.charging)
			useLegacyLoopState(t, mock, &mockConf{upper: 70, lower: 40, chargeOnceTarget: tt.target})

			if !maintainLoopForced() {
				t.Fatal("legacy maintain loop failed")
			}
			charging, err := mock.IsChargingEnabled()
			if err != nil {
				t.Fatal(err)
			}
			if charging != tt.wantCharging {
				t.Fatalf("charging = %v, want %v", charging, tt.wantCharging)
			}
		})
	}
}

// firmwareChargeMock builds an SMC mock for the firmware charge-control backend.
func firmwareChargeMock(t *testing.T) *smc.AppleSMC {
	t.Helper()
	value := func(key string, dataType gosmc.DataType, data ...byte) gosmc.Value {
		v, err := gosmc.NewValue(key, dataType, data)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	mock := smc.NewMockValues(
		value(smc.FirmwareChargeLimitActivationKey, gosmc.TypeUInt8, 0),
		value(smc.FirmwareChargeLimitUpperKey, gosmc.TypeUInt32, 0, 0, 0, 0),
		value(smc.FirmwareChargeLimitLowerKey, gosmc.TypeUInt32, 0, 0, 0, 0),
	)
	if err := mock.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mock.Close() })
	return mock
}

func useFirmwareLoopState(t *testing.T, mock *smc.AppleSMC, configured *mockConf) {
	t.Helper()
	previousSMC, previousConf, previousCapabilities := smcConn, conf, capabilities
	previousState := calibrationState
	t.Cleanup(func() {
		smcConn, conf, capabilities = previousSMC, previousConf, previousCapabilities
		calibrationState = previousState
	})
	smcConn = mock
	conf = configured
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	capabilities = compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlFirmware,
	}
}

func TestFirmwareMaintainLoopAppliesChargeOnceBand(t *testing.T) {
	mock := firmwareChargeMock(t)
	configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 70}
	useFirmwareLoopState(t, mock, configured)

	// The firmware API rejects lower >= upper, so a one-time charge to the
	// configured limit uses the narrowest legal band below it.
	if !maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	state, err := mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Lower != 69 || state.Upper != 70 {
		t.Fatalf("firmware state = %+v, want active 69/70", state)
	}

	// Charging to full hands the range back to the firmware default.
	configured.chargeOnceTarget = 100
	if !maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	state, err = mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if state.Active {
		t.Fatalf("firmware limit is still active during a one-time charge to 100%%: %+v", state)
	}

	// Once the one-time charge ends, the configured band applies again.
	configured.chargeOnceTarget = 0
	if !maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	state, err = mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Lower != 40 || state.Upper != 70 {
		t.Fatalf("firmware state = %+v, want active 40/70", state)
	}
}

func TestAdapterModeOneTimeChargeRestoresWallPower(t *testing.T) {
	configured := &mockConf{upper: 60, lower: 55, chargeOnceTarget: 100}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	useAdapterCapabilities(t)
	adapterMockSMC(t, 65, true, false)
	native := &fakeNativeLimit{supported: true, limit: 80, enabled: true}
	useFakeNativeLimit(t, native)
	fake := &fakeCharger{enabled: false}
	useCharger(t, fake)

	if !maintainLoopForced() || fake.enables != 1 || !fake.enabled || native.enabled {
		t.Fatalf("adapter-mode charge must clear native limit and restore wall power: charger=%+v native=%+v", fake, native)
	}
}

func TestAdapterChargeOnceAdmissionFailureRestoresNativeLimit(t *testing.T) {
	configured := &mockConf{upper: 80, lower: 78}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	capabilities = compatibility.Capabilities{ChargingControl: true, ChargeControlMode: compatibility.ChargeControlAdapter}
	gatedSMC(t)              // adapter key works, but maintain cannot read battery charge
	stubBatteryCharge(t, 60) // admission can read it through the test seam
	previousCharger := charger
	t.Cleanup(func() { charger = previousCharger })
	charger = adapterSwitch{}
	fake := &fakeNativeLimit{supported: true, limit: 80, enabled: true}
	useFakeNativeLimit(t, fake)

	response := postChargeOnce(chargeOnceFullPath)
	if response.Code != http.StatusInternalServerError || configured.chargeOnceTarget != 0 || !fake.enabled || fake.limit != 80 {
		t.Fatalf("failed adapter enforcement must restore native limit and target: status=%d, target=%d, native=%+v", response.Code, configured.chargeOnceTarget, fake)
	}
}

func TestNativeChargeOnceToFullUsesNativeLimit(t *testing.T) {
	configured := &mockConf{upper: 80, lower: 78}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	useNativeCapabilities(t, 80, 85, 90, 95, 100)
	fake := &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}, limit: 80, enabled: true}
	useFakeNativeLimit(t, fake)
	stubBatteryCharge(t, 70)

	if response := postChargeOnce(chargeOnceLimitPath); response.Code != http.StatusConflict {
		t.Fatalf("native charge now must be rejected: status=%d body=%s", response.Code, response.Body.String())
	}
	if configured.chargeOnceTarget != 0 {
		t.Fatal("rejected native charge now must not persist a target")
	}
	if response := postChargeOnce(chargeOnceFullPath); response.Code != http.StatusCreated {
		t.Fatalf("native charge full must be accepted: status=%d body=%s", response.Code, response.Body.String())
	}
	if configured.chargeOnceTarget != 100 || fake.disables != 1 || fake.enabled {
		t.Fatalf("native limit must be disabled until full: target=%d, limit=%+v", configured.chargeOnceTarget, fake)
	}
	configured.chargeOnceTarget = 0
	if !maintainLoopForced() || !fake.enabled || fake.limit != 80 {
		t.Fatalf("configured native limit must resume after completion: %+v", fake)
	}
}

func TestChargeOnceReachedTarget(t *testing.T) {
	tests := []struct {
		name   string
		mode   compatibility.ChargeControlMode
		target int
		charge int
		want   bool
	}{
		{name: "legacy stops at the target", mode: compatibility.ChargeControlLegacy, target: 80, charge: 80, want: true},
		{name: "legacy does not accept one percent short", mode: compatibility.ChargeControlLegacy, target: 80, charge: 79},
		// The firmware band for a one-time charge to 80% is 79/80, and the
		// firmware never resumes charging at 79%.
		{name: "firmware accepts the top of its band", mode: compatibility.ChargeControlFirmware, target: 80, charge: 79, want: true},
		{name: "firmware keeps charging below the band", mode: compatibility.ChargeControlFirmware, target: 80, charge: 78},
		// A one-time charge to 100% deactivates the limit instead of narrowing
		// the band, so it has no percent to give away.
		{name: "firmware charge to full needs 100%", mode: compatibility.ChargeControlFirmware, target: 100, charge: 99},
		{name: "firmware charge to full reached", mode: compatibility.ChargeControlFirmware, target: 100, charge: 100, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := capabilities
			t.Cleanup(func() { capabilities = previous })
			capabilities = compatibility.Capabilities{ChargingControl: true, ChargeControlMode: tt.mode}

			if got := chargeOnceReachedTarget(tt.target, tt.charge); got != tt.want {
				t.Fatalf("chargeOnceReachedTarget(%d, %d) = %v, want %v", tt.target, tt.charge, got, tt.want)
			}
		})
	}
}

func TestFirmwareChargeOnceCompletesAtTheTopOfItsBand(t *testing.T) {
	// A one-time charge to the 80% limit started at 79% cannot move: the
	// firmware only resumes charging below the lower bound of the 79/80 band it
	// gets. It must still end and hand the configured band back.
	mock := firmwareChargeMock(t)
	configured := &mockConf{upper: 80, lower: 78, chargeOnceTarget: 80}
	useFirmwareLoopState(t, mock, configured)
	stubBatteryCharge(t, 79)

	if !maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	state, err := mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Lower != 79 || state.Upper != 80 {
		t.Fatalf("firmware state = %+v, want active 79/80", state)
	}

	if !completeChargeOnce(configured) {
		t.Fatal("completeChargeOnce() = false, want true at the top of the one-time band")
	}
	if configured.chargeOnceTarget != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0", configured.chargeOnceTarget)
	}

	// The configured band applies again, so the persistent limit is restored.
	if !maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	state, err = mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Lower != 78 || state.Upper != 80 {
		t.Fatalf("firmware state = %+v, want active 78/80", state)
	}
}

func TestCompleteChargeOnceKeepsTheTargetWhenSaveFails(t *testing.T) {
	configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 70, saveErr: errors.New("disk full")}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	capabilities.ChargeControlMode = compatibility.ChargeControlLegacy
	stubBatteryCharge(t, 70)

	if completeChargeOnce(configured) {
		t.Fatal("completeChargeOnce() = true after a failed save, want false")
	}
	if configured.chargeOnceTarget != 70 {
		t.Fatalf("chargeOnceTarget = %d, want 70 after a failed save", configured.chargeOnceTarget)
	}

	configured.saveErr = nil
	if !completeChargeOnce(configured) {
		t.Fatal("completeChargeOnce() retry = false, want true")
	}
	if configured.chargeOnceTarget != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0 after a successful retry", configured.chargeOnceTarget)
	}
}

func TestCancelChargeOnceKeepsTheTargetWhenSaveFails(t *testing.T) {
	// The config file still holds the target after a failed save, so dropping
	// it from memory would make a retry report that nothing is running while a
	// restart resumes the cancelled one-time charge.
	configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 100, saveErr: errors.New("disk full")}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)

	response := postChargeOnce(chargeOnceCancelPath)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if configured.chargeOnceTarget != 100 {
		t.Fatalf("chargeOnceTarget = %d, want 100 after a failed save", configured.chargeOnceTarget)
	}

	// The retry after a working disk cancels as usual.
	configured.saveErr = nil
	response = postChargeOnce(chargeOnceCancelPath)
	if response.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if configured.chargeOnceTarget != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0", configured.chargeOnceTarget)
	}
}

func TestStartChargeOnceKeepsNoTargetWhenSaveFails(t *testing.T) {
	configured := &mockConf{upper: 70, lower: 40, saveErr: errors.New("disk full")}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	stubBatteryCharge(t, 58)

	response := postChargeOnce(chargeOnceLimitPath)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if configured.chargeOnceTarget != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0 after a failed save", configured.chargeOnceTarget)
	}
}

func TestForceDischargeRejectsChargeOnce(t *testing.T) {
	// The other half of the conflict: a one-time charge that is already running
	// must not be left charging against a disabled power adapter.
	tests := []struct {
		name string
		body string
		path string
	}{
		{name: "indefinitely", path: "/adapter", body: "false"},
		{name: "for a duration", path: "/adapter/disable", body: `"1h0m0s"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previousDisable := smcDisableAdapter
			t.Cleanup(func() { smcDisableAdapter = previousDisable })
			called := false
			smcDisableAdapter = func() error {
				called = true
				return nil
			}

			configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 100}
			useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
			capabilities = compatibility.Capabilities{ChargingControl: true, AdapterControl: true}

			request := httptest.NewRequest(http.MethodPut, tt.path, strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			setupRoutes().ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), ErrChargeOnceInProgress.Error()) {
				t.Fatalf("response does not explain the conflict: %s", response.Body.String())
			}
			if called {
				t.Fatal("rejected force discharge still cut power")
			}
			if configured.chargeOnceTarget != 100 || !configured.adapterDisableUntil.IsZero() {
				t.Fatalf("rejected force discharge changed state: %+v", configured)
			}
		})
	}
}

func TestEnablingTheAdapterKeepsChargeOnce(t *testing.T) {
	previousEnable := smcEnableAdapter
	t.Cleanup(func() { smcEnableAdapter = previousEnable })
	smcEnableAdapter = func() error { return nil }

	configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 100}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	capabilities = compatibility.Capabilities{ChargingControl: true, AdapterControl: true}

	request := httptest.NewRequest(http.MethodPut, "/adapter", strings.NewReader("true"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if configured.chargeOnceTarget != 100 {
		t.Fatalf("chargeOnceTarget = %d, want 100", configured.chargeOnceTarget)
	}
}

func TestStartChargeOnceWithAHandEditedLimit(t *testing.T) {
	// config.File rejects a target below 10%, and ChargeOnceTarget reads such a
	// value as absent. A hand-edited limit below 10% must therefore be refused
	// before it reaches the config, not panic its way into a 500.
	configured := config.NewFileFromConfig(
		&config.RawFileConfig{Limit: ptr.To(5), LowerLimitDelta: ptr.To(2)},
		filepath.Join(t.TempDir(), "batt.json"),
	)
	previousConf, previousCapabilities := conf, capabilities
	previousState, previousStatePath := calibrationState, calibrationStatePath
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState, calibrationStatePath = previousState, previousStatePath
	})
	conf = configured
	capabilities = compatibility.Capabilities{ChargingControl: true}
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	calibrationStatePath = ""
	stubBatteryCharge(t, 3)

	for _, path := range []string{chargeOnceLimitPath, chargeOnceFullPath} {
		response := postChargeOnce(path)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want %d; body: %s", path, response.Code, http.StatusBadRequest, response.Body.String())
		}
		// gin escapes the "<percentage>" hint, so match the reason only.
		if !strings.Contains(response.Body.String(), "charge limit is below 10%") {
			t.Fatalf("%s response does not explain the rejection: %s", path, response.Body.String())
		}
		if configured.ChargeOnceTarget() != 0 {
			t.Fatalf("%s started a one-time charge to %d%%", path, configured.ChargeOnceTarget())
		}
	}
}

// stubAdapterEnabled replaces the adapter-state test seam for one test and
// reports whether the seam was read.
func stubAdapterEnabled(t *testing.T, enabled bool, err error) *bool {
	t.Helper()
	previous := smcIsAdapterEnabled
	t.Cleanup(func() { smcIsAdapterEnabled = previous })
	asked := false
	smcIsAdapterEnabled = func() (bool, error) {
		asked = true
		return enabled, err
	}
	return &asked
}

func TestStartChargeOnceChecksThePowerAdapter(t *testing.T) {
	// "batt adapter disable" cuts the power a one-time charge needs and writes
	// no deadline that could restore it, so the config alone cannot see it.
	tests := []struct {
		name                string
		path                string
		adapterControl      bool
		adapterEnabled      bool
		adapterErr          error
		adapterDisableUntil time.Time
		wantCode            int
		wantBody            string
		wantTarget          int
		wantAsked           bool
	}{
		{
			name:           "disabled indefinitely",
			adapterControl: true,
			wantCode:       http.StatusBadRequest,
			wantBody:       ErrAdapterDisabled.Error(),
			wantAsked:      true,
		},
		{
			name:           "disabled indefinitely, full requested",
			path:           chargeOnceFullPath,
			adapterControl: true,
			wantCode:       http.StatusBadRequest,
			wantBody:       ErrAdapterDisabled.Error(),
			wantAsked:      true,
		},
		{
			// A failed read is the daemon's fault, not the caller's, and it
			// must not admit a charge that may be unable to progress.
			name:           "adapter state unreadable",
			adapterControl: true,
			adapterEnabled: true,
			adapterErr:     errors.New("smc read failed"),
			wantCode:       http.StatusInternalServerError,
			wantBody:       "smc read failed",
			wantAsked:      true,
		},
		{
			name:           "adapter enabled",
			adapterControl: true,
			adapterEnabled: true,
			wantCode:       http.StatusCreated,
			wantTarget:     70,
			wantAsked:      true,
		},
		{
			// A temporary disable names its own deadline, so it keeps its
			// message and never reaches the hardware read.
			name:                "temporary disable is pending",
			adapterControl:      true,
			adapterDisableUntil: time.Now().Add(time.Hour),
			wantCode:            http.StatusBadRequest,
			wantBody:            ErrTemporaryAdapterDisableInProgress.Error(),
		},
		{
			// Hardware without adapter control cannot have its power cut, so
			// the admission never asks.
			name:       "hardware without adapter control",
			wantCode:   http.StatusCreated,
			wantTarget: 70,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path
			if path == "" {
				path = chargeOnceLimitPath
			}
			configured := &mockConf{upper: 70, lower: 40, adapterDisableUntil: tt.adapterDisableUntil}
			useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
			capabilities = compatibility.Capabilities{ChargingControl: true, AdapterControl: tt.adapterControl}
			stubBatteryCharge(t, 58)
			useChargeOnceLegacyAdmissionSMC(t, 58)
			asked := stubAdapterEnabled(t, tt.adapterEnabled, tt.adapterErr)

			response := postChargeOnce(path)
			if response.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, tt.wantCode, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), tt.wantBody) {
				t.Fatalf("response does not explain the outcome: %s", response.Body.String())
			}
			if configured.chargeOnceTarget != tt.wantTarget {
				t.Fatalf("chargeOnceTarget = %d, want %d", configured.chargeOnceTarget, tt.wantTarget)
			}
			if *asked != tt.wantAsked {
				t.Fatalf("read the power adapter state = %v, want %v", *asked, tt.wantAsked)
			}
		})
	}
}

func TestChargeOnceConflictKeepsTheAdapterReadError(t *testing.T) {
	// The request answers with a server error, so the cause has to survive for
	// the log line and for anyone matching on it.
	configured := &mockConf{upper: 70, lower: 40}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	capabilities = compatibility.Capabilities{ChargingControl: true, AdapterControl: true}
	readErr := errors.New("smc read failed")
	stubAdapterEnabled(t, true, readErr)

	err := chargeOnceConflict(configured)
	if !errors.Is(err, readErr) {
		t.Fatalf("conflict error = %v, want it to wrap %v", err, readErr)
	}
}

func TestStartChargeOnceRejectsATargetAlreadyReached(t *testing.T) {
	// The firmware drives a one-time charge below 100% with the narrowest legal
	// band, target-1/target, and does not resume charging at target-1. Starting
	// there would create a one-time charge that the next maintain loop ends
	// right away, so admission asks the same question as completion.
	tests := []struct {
		name       string
		mode       compatibility.ChargeControlMode
		path       string
		charge     int
		wantCode   int
		wantBody   string
		wantTarget int
	}{
		{
			name:     "firmware one percent below the limit",
			mode:     compatibility.ChargeControlFirmware,
			path:     chargeOnceLimitPath,
			charge:   79,
			wantCode: http.StatusBadRequest,
			wantBody: "already at 79%",
		},
		{
			name:       "legacy one percent below the limit",
			mode:       compatibility.ChargeControlLegacy,
			path:       chargeOnceLimitPath,
			charge:     79,
			wantCode:   http.StatusCreated,
			wantTarget: 80,
		},
		{
			// A one-time charge to 100% deactivates the limit instead of
			// narrowing a band, so it has no percent to give away.
			name:       "firmware one percent below full",
			mode:       compatibility.ChargeControlFirmware,
			path:       chargeOnceFullPath,
			charge:     99,
			wantCode:   http.StatusCreated,
			wantTarget: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configured := &mockConf{upper: 80, lower: 78}
			useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
			capabilities = compatibility.Capabilities{ChargingControl: true, ChargeControlMode: tt.mode}
			stubBatteryCharge(t, tt.charge)
			// An admitted one-time charge runs the maintain loop right away, so
			// the backend under test needs its SMC mock.
			previousSMC := smcConn
			t.Cleanup(func() { smcConn = previousSMC })
			if tt.mode == compatibility.ChargeControlFirmware {
				smcConn = firmwareChargeMock(t)
			} else {
				smcConn = legacyChargeMock(t, tt.charge, false)
			}

			response := postChargeOnce(tt.path)
			if response.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, tt.wantCode, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), tt.wantBody) {
				t.Fatalf("response does not explain the outcome: %s", response.Body.String())
			}
			if configured.chargeOnceTarget != tt.wantTarget {
				t.Fatalf("chargeOnceTarget = %d, want %d", configured.chargeOnceTarget, tt.wantTarget)
			}
		})
	}
}
