package daemon

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/utils/ptr"
)

// The one-time charge routes, named so the tests below stay readable.
const (
	chargeOnceLimitPath  = "/charge-once/limit"
	chargeOnceFullPath   = "/charge-once/full"
	chargeOnceCancelPath = "/charge-once/cancel"
)

// chargeOnceConf is an in-memory config with the given band.
func chargeOnceConf(upper, lower int) *memConfig {
	c := newMemConfig()
	c.upper, c.lower = upper, lower
	return c
}

// newChargeOnceFixture is a legacy Mac at charge percent with charging off,
// running conf, with a calibration in phase.
func newChargeOnceFixture(t *testing.T, conf *memConfig, charge int, phase calibration.Phase) *fixture {
	t.Helper()
	f := newFixture(t, firmwareLegacy, withConfig(conf), withCharge(charge), withCharging(false))
	f.setCalibration(&calibration.State{Phase: phase})
	return f
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
			got, err := resolveChargeOnceTarget(chargeOnceConf(tt.upper, tt.upper-2), tt.full)
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

func TestStartChargeOnceToLimitInsideHysteresisGap(t *testing.T) {
	// 58% with a persistent 40-70% band: the charge sits in the gap where batt
	// normally waits, so this is the case the feature exists for.
	conf := chargeOnceConf(70, 40)
	f := newChargeOnceFixture(t, conf, 58, calibration.PhaseIdle)

	f.mustServe(http.MethodPost, chargeOnceLimitPath, "", http.StatusCreated)
	if conf.ChargeOnceTarget() != 70 {
		t.Fatalf("chargeOnceTarget = %d, want 70", conf.ChargeOnceTarget())
	}
	if conf.UpperLimit() != 70 || conf.LowerLimit() != 40 {
		t.Fatalf("configured band changed to %d/%d, want 70/40", conf.UpperLimit(), conf.LowerLimit())
	}
	if !f.smc.chargingEnabled(t) {
		t.Fatal("the first enforcement pass must start charging inside the gap")
	}
}

func TestStartChargeOnceToFullKeepsConfiguredLimit(t *testing.T) {
	conf := chargeOnceConf(70, 68)
	f := newChargeOnceFixture(t, conf, 70, calibration.PhaseIdle)

	f.mustServe(http.MethodPost, chargeOnceFullPath, "", http.StatusCreated)
	if conf.ChargeOnceTarget() != 100 {
		t.Fatalf("chargeOnceTarget = %d, want 100", conf.ChargeOnceTarget())
	}
	// Unlike "batt disable --for", a one-time charge never writes limit=100, so
	// there is no saved limit that a crash could strand.
	if conf.UpperLimit() != 70 || !conf.DisableUntil().IsZero() {
		t.Fatalf("configured limit changed: %+v", conf.snapshot())
	}
}

func TestStartChargeOnceRejections(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		conf    func() *memConfig
		phase   calibration.Phase
		charge  int
		wantErr string
	}{
		{
			name:    "already at the target",
			path:    chargeOnceLimitPath,
			conf:    func() *memConfig { return chargeOnceConf(70, 40) },
			charge:  70,
			wantErr: "already at 70%",
		},
		{
			name:    "already full",
			path:    chargeOnceFullPath,
			conf:    func() *memConfig { return chargeOnceConf(70, 68) },
			charge:  100,
			wantErr: "already at 100%",
		},
		{
			name:    "charge limit disabled",
			path:    chargeOnceFullPath,
			conf:    func() *memConfig { return chargeOnceConf(100, 98) },
			charge:  50,
			wantErr: "batt is not limiting charging",
		},
		{
			name:    "calibration owns the charge limit",
			path:    chargeOnceLimitPath,
			conf:    func() *memConfig { return chargeOnceConf(70, 40) },
			phase:   calibration.PhaseCharge,
			charge:  58,
			wantErr: ErrCalibrationControlsChargeLimit.Error(),
		},
		{
			name: "temporary disable is pending",
			path: chargeOnceFullPath,
			conf: func() *memConfig {
				c := chargeOnceConf(100, 68)
				c.SetDisableTimer(time.Now().Add(time.Hour), 70)
				return c
			},
			charge:  58,
			wantErr: ErrTemporaryDisableInProgress.Error(),
		},
		{
			name: "one-time charge already running",
			path: chargeOnceFullPath,
			conf: func() *memConfig {
				c := chargeOnceConf(70, 68)
				c.SetChargeOnceTarget(70)
				return c
			},
			charge:  58,
			wantErr: ErrChargeOnceInProgress.Error(),
		},
		{
			// Force discharge cuts the power the one-time charge needs, so it
			// would sit there without making progress.
			name: "temporary adapter disable is pending",
			path: chargeOnceLimitPath,
			conf: func() *memConfig {
				c := chargeOnceConf(70, 40)
				c.SetAdapterDisableTimer(time.Now().Add(time.Hour))
				return c
			},
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
			conf := tt.conf()
			before := conf.snapshot()
			f := newChargeOnceFixture(t, conf, tt.charge, phase)

			response := f.post(tt.path)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), tt.wantErr) {
				t.Fatalf("response does not explain the rejection: %s", response.Body.String())
			}
			if after := conf.snapshot(); after != before {
				t.Fatalf("rejected request mutated config: %+v, want %+v", after, before)
			}
		})
	}
}

func TestCancelChargeOnceRestoresConfiguredBehavior(t *testing.T) {
	conf := chargeOnceConf(70, 40)
	conf.SetChargeOnceTarget(100)
	f := newChargeOnceFixture(t, conf, 58, calibration.PhaseIdle)

	f.mustServe(http.MethodPost, chargeOnceCancelPath, "", http.StatusOK)
	if conf.ChargeOnceTarget() != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0", conf.ChargeOnceTarget())
	}
	if conf.UpperLimit() != 70 || conf.LowerLimit() != 40 {
		t.Fatalf("configured band changed to %d/%d, want 70/40", conf.UpperLimit(), conf.LowerLimit())
	}
}

func TestCancelChargeOnceWithoutSession(t *testing.T) {
	f := newChargeOnceFixture(t, chargeOnceConf(70, 40), 58, calibration.PhaseIdle)

	response := f.post(chargeOnceCancelPath)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), ErrChargeOnceNotRunning.Error()) {
		t.Fatalf("response does not explain the rejection: %s", response.Body.String())
	}
}

func TestSetLimitCancelsChargeOnce(t *testing.T) {
	conf := chargeOnceConf(70, 40)
	conf.SetChargeOnceTarget(100)
	f := newFixture(t, firmwareGatedNoAdapter, withConfig(conf), withCharge(58),
		withNative(&fakeNativeLimit{limit: 100}), withCapabilities(nativeCaps(60, 70, 100)))

	f.mustServe(http.MethodPut, "/limit", "60", http.StatusCreated)
	if conf.ChargeOnceTarget() != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0 after an explicit limit change", conf.ChargeOnceTarget())
	}
	if conf.UpperLimit() != 60 {
		t.Fatalf("upper = %d, want 60", conf.UpperLimit())
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
			conf := chargeOnceConf(70, 40)
			conf.SetChargeOnceTarget(100)
			conf.failSave(errors.New("disk full"))
			f := newChargeOnceFixture(t, conf, 58, calibration.PhaseIdle)

			response := f.put(tt.path, tt.body)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500: %s", response.Code, response.Body.String())
			}
			if conf.ChargeOnceTarget() != 100 || conf.UpperLimit() != 70 || !conf.DisableUntil().IsZero() {
				t.Fatalf("failed save must keep original target and limit: %+v", conf.snapshot())
			}
		})
	}
}

func TestStartCalibrationRejectsChargeOnce(t *testing.T) {
	conf := chargeOnceConf(70, 40)
	conf.SetChargeOnceTarget(100)
	f := newChargeOnceFixture(t, conf, 58, calibration.PhaseIdle)

	response := f.post("/calibration/start")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), ErrChargeOnceInProgress.Error()) {
		t.Fatalf("response does not explain the conflict: %s", response.Body.String())
	}
	if f.phase() != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", f.phase())
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
			conf := chargeOnceConf(70, 40)
			conf.SetChargeOnceTarget(tt.target)
			// The completion charge depends on the backend, so name it.
			f := newChargeOnceFixture(t, conf, tt.charge, phase)

			if got := f.d.completeChargeOnce(); got != tt.want {
				t.Fatalf("completeChargeOnce() = %v, want %v", got, tt.want)
			}
			if conf.ChargeOnceTarget() != tt.wantLeft {
				t.Fatalf("chargeOnceTarget = %d, want %d", conf.ChargeOnceTarget(), tt.wantLeft)
			}
			if conf.UpperLimit() != 70 || conf.LowerLimit() != 40 {
				t.Fatalf("configured band changed to %d/%d, want 70/40", conf.UpperLimit(), conf.LowerLimit())
			}
		})
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
			conf := chargeOnceConf(70, 40)
			conf.SetChargeOnceTarget(tt.target)
			f := newFixture(t, firmwareLegacy, withConfig(conf), withCharge(tt.charge), withCharging(tt.charging))

			if !f.d.maintainLoopForced() {
				t.Fatal("legacy maintain loop failed")
			}
			if charging := f.smc.chargingEnabled(t); charging != tt.wantCharging {
				t.Fatalf("charging = %v, want %v", charging, tt.wantCharging)
			}
		})
	}
}

func TestFirmwareMaintainLoopAppliesChargeOnceBand(t *testing.T) {
	conf := chargeOnceConf(70, 40)
	conf.SetChargeOnceTarget(70)
	f := newFixture(t, firmwareManaged, withConfig(conf))

	// The firmware API rejects lower >= upper, so a one-time charge to the
	// configured limit uses the narrowest legal band below it.
	if !f.d.maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	if state := f.smc.firmwareLimit(t); !state.Active || state.Lower != 69 || state.Upper != 70 {
		t.Fatalf("firmware state = %+v, want active 69/70", state)
	}

	// Charging to full hands the range back to the firmware default.
	conf.SetChargeOnceTarget(100)
	if !f.d.maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	if state := f.smc.firmwareLimit(t); state.Active {
		t.Fatalf("firmware limit is still active during a one-time charge to 100%%: %+v", state)
	}

	// Once the one-time charge ends, the configured band applies again.
	conf.ClearChargeOnceTarget()
	if !f.d.maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	if state := f.smc.firmwareLimit(t); !state.Active || state.Lower != 40 || state.Upper != 70 {
		t.Fatalf("firmware state = %+v, want active 40/70", state)
	}
}

func TestAdapterModeOneTimeChargeRestoresWallPower(t *testing.T) {
	conf := chargeOnceConf(60, 55)
	conf.SetChargeOnceTarget(100)
	native := &fakeNativeLimit{supported: true, limit: 80, enabled: true}
	f := newFixture(t, firmwareGated, withConfig(conf), withCharge(65), withAdapter(false),
		withNative(native), withCapabilities(adapterCaps()))
	fake := &fakeCharger{enabled: false}
	f.useCharger(fake)

	if !f.d.maintainLoopForced() || fake.enables != 1 || !fake.enabled || native.enabled {
		t.Fatalf("adapter-mode charge must clear native limit and restore wall power: charger=%+v native=%+v", fake, native)
	}
}

func TestAdapterChargeOnceAdmissionFailureRestoresNativeLimit(t *testing.T) {
	// Admission can read the battery charge, but the adapter loop's first
	// enforcement pass fails, so the request must undo everything it did.
	conf := chargeOnceConf(80, 78)
	fake := &fakeNativeLimit{supported: true, limit: 80, enabled: true}
	f := newFixture(t, firmwareGated, withConfig(conf), withCharge(60), withNative(fake),
		withCapabilities(compatibility.Capabilities{ChargingControl: true, ChargeControlMode: compatibility.ChargeControlAdapter}))
	f.smc.fail("IsPluggedIn", errors.New("AC key gated"))

	response := f.post(chargeOnceFullPath)
	if response.Code != http.StatusInternalServerError || conf.ChargeOnceTarget() != 0 || !fake.enabled || fake.limit != 80 {
		t.Fatalf("failed adapter enforcement must restore native limit and target: status=%d, target=%d, native=%+v", response.Code, conf.ChargeOnceTarget(), fake)
	}
}

func TestNativeChargeOnceToFullUsesNativeLimit(t *testing.T) {
	conf := chargeOnceConf(80, 78)
	fake := &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}, limit: 80, enabled: true}
	f := newFixture(t, firmwareGated, withConfig(conf), withCharge(70), withNative(fake),
		withCapabilities(nativeCaps(80, 85, 90, 95, 100)))

	if response := f.post(chargeOnceLimitPath); response.Code != http.StatusConflict {
		t.Fatalf("native charge now must be rejected: status=%d body=%s", response.Code, response.Body.String())
	}
	if conf.ChargeOnceTarget() != 0 {
		t.Fatal("rejected native charge now must not persist a target")
	}
	if response := f.post(chargeOnceFullPath); response.Code != http.StatusCreated {
		t.Fatalf("native charge full must be accepted: status=%d body=%s", response.Code, response.Body.String())
	}
	if conf.ChargeOnceTarget() != 100 || fake.disables != 1 || fake.enabled {
		t.Fatalf("native limit must be disabled until full: target=%d, limit=%+v", conf.ChargeOnceTarget(), fake)
	}
	conf.ClearChargeOnceTarget()
	if !f.d.maintainLoopForced() || !fake.enabled || fake.limit != 80 {
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

	f := newFixture(t, firmwareLegacy)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.setCaps(compatibility.Capabilities{ChargingControl: true, ChargeControlMode: tt.mode})

			if got := f.d.chargeOnceReachedTarget(tt.target, tt.charge); got != tt.want {
				t.Fatalf("chargeOnceReachedTarget(%d, %d) = %v, want %v", tt.target, tt.charge, got, tt.want)
			}
		})
	}
}

func TestFirmwareChargeOnceCompletesAtTheTopOfItsBand(t *testing.T) {
	// A one-time charge to the 80% limit started at 79% cannot move: the
	// firmware only resumes charging below the lower bound of the 79/80 band it
	// gets. It must still end and hand the configured band back.
	conf := chargeOnceConf(80, 78)
	conf.SetChargeOnceTarget(80)
	f := newFixture(t, firmwareManaged, withConfig(conf), withCharge(79))

	if !f.d.maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	if state := f.smc.firmwareLimit(t); !state.Active || state.Lower != 79 || state.Upper != 80 {
		t.Fatalf("firmware state = %+v, want active 79/80", state)
	}

	if !f.d.completeChargeOnce() {
		t.Fatal("completeChargeOnce() = false, want true at the top of the one-time band")
	}
	if conf.ChargeOnceTarget() != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0", conf.ChargeOnceTarget())
	}

	// The configured band applies again, so the persistent limit is restored.
	if !f.d.maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	if state := f.smc.firmwareLimit(t); !state.Active || state.Lower != 78 || state.Upper != 80 {
		t.Fatalf("firmware state = %+v, want active 78/80", state)
	}
}

func TestCompleteChargeOnceKeepsTheTargetWhenSaveFails(t *testing.T) {
	conf := chargeOnceConf(70, 40)
	conf.SetChargeOnceTarget(70)
	conf.failSave(errors.New("disk full"))
	f := newChargeOnceFixture(t, conf, 70, calibration.PhaseIdle)

	if f.d.completeChargeOnce() {
		t.Fatal("completeChargeOnce() = true after a failed save, want false")
	}
	if conf.ChargeOnceTarget() != 70 {
		t.Fatalf("chargeOnceTarget = %d, want 70 after a failed save", conf.ChargeOnceTarget())
	}

	conf.failSave(nil)
	if !f.d.completeChargeOnce() {
		t.Fatal("completeChargeOnce() retry = false, want true")
	}
	if conf.ChargeOnceTarget() != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0 after a successful retry", conf.ChargeOnceTarget())
	}
}

func TestCancelChargeOnceKeepsTheTargetWhenSaveFails(t *testing.T) {
	// The config file still holds the target after a failed save, so dropping
	// it from memory would make a retry report that nothing is running while a
	// restart resumes the cancelled one-time charge.
	conf := chargeOnceConf(70, 40)
	conf.SetChargeOnceTarget(100)
	conf.failSave(errors.New("disk full"))
	f := newChargeOnceFixture(t, conf, 58, calibration.PhaseIdle)

	response := f.post(chargeOnceCancelPath)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if conf.ChargeOnceTarget() != 100 {
		t.Fatalf("chargeOnceTarget = %d, want 100 after a failed save", conf.ChargeOnceTarget())
	}

	// The retry after a working disk cancels as usual.
	conf.failSave(nil)
	response = f.post(chargeOnceCancelPath)
	if response.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if conf.ChargeOnceTarget() != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0", conf.ChargeOnceTarget())
	}
}

func TestStartChargeOnceKeepsNoTargetWhenSaveFails(t *testing.T) {
	conf := chargeOnceConf(70, 40)
	conf.failSave(errors.New("disk full"))
	f := newChargeOnceFixture(t, conf, 58, calibration.PhaseIdle)

	response := f.post(chargeOnceLimitPath)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if conf.ChargeOnceTarget() != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0 after a failed save", conf.ChargeOnceTarget())
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
			conf := chargeOnceConf(70, 40)
			conf.SetChargeOnceTarget(100)
			f := newChargeOnceFixture(t, conf, 58, calibration.PhaseIdle)

			response := f.put(tt.path, tt.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), ErrChargeOnceInProgress.Error()) {
				t.Fatalf("response does not explain the conflict: %s", response.Body.String())
			}
			if f.smc.callCount("DisableAdapter") != 0 {
				t.Fatal("rejected force discharge still cut power")
			}
			if conf.ChargeOnceTarget() != 100 || !conf.AdapterDisableUntil().IsZero() {
				t.Fatalf("rejected force discharge changed state: %+v", conf.snapshot())
			}
		})
	}
}

func TestEnablingTheAdapterKeepsChargeOnce(t *testing.T) {
	conf := chargeOnceConf(70, 40)
	conf.SetChargeOnceTarget(100)
	f := newChargeOnceFixture(t, conf, 58, calibration.PhaseIdle)

	f.mustServe(http.MethodPut, "/adapter", "true", http.StatusCreated)
	if conf.ChargeOnceTarget() != 100 {
		t.Fatalf("chargeOnceTarget = %d, want 100", conf.ChargeOnceTarget())
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
	f := newFixture(t, firmwareLegacy, withConfig(configured), withCharge(3), withCharging(false))

	for _, path := range []string{chargeOnceLimitPath, chargeOnceFullPath} {
		response := f.post(path)
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
			conf := chargeOnceConf(70, 40)
			conf.SetAdapterDisableTimer(tt.adapterDisableUntil)
			f := newFixture(t, firmwareLegacy, withConfig(conf), withCharge(58), withCharging(false),
				withAdapter(tt.adapterEnabled),
				withCapabilities(compatibility.Capabilities{
					ChargingControl:   true,
					ChargeControlMode: compatibility.ChargeControlLegacy,
					AdapterControl:    tt.adapterControl,
				}))
			f.smc.fail("IsAdapterEnabled", tt.adapterErr)

			response := f.post(path)
			if response.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, tt.wantCode, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), tt.wantBody) {
				t.Fatalf("response does not explain the outcome: %s", response.Body.String())
			}
			if conf.ChargeOnceTarget() != tt.wantTarget {
				t.Fatalf("chargeOnceTarget = %d, want %d", conf.ChargeOnceTarget(), tt.wantTarget)
			}
			if asked := f.smc.callCount("IsAdapterEnabled") > 0; asked != tt.wantAsked {
				t.Fatalf("read the power adapter state = %v, want %v", asked, tt.wantAsked)
			}
		})
	}
}

func TestChargeOnceConflictKeepsTheAdapterReadError(t *testing.T) {
	// The request answers with a server error, so the cause has to survive for
	// the log line and for anyone matching on it.
	f := newChargeOnceFixture(t, chargeOnceConf(70, 40), 58, calibration.PhaseIdle)
	readErr := errors.New("smc read failed")
	f.smc.fail("IsAdapterEnabled", readErr)

	err := f.d.chargeOnceConflict()
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
		fw         firmware
		path       string
		charge     int
		wantCode   int
		wantBody   string
		wantTarget int
	}{
		{
			name:     "firmware one percent below the limit",
			fw:       firmwareManaged,
			path:     chargeOnceLimitPath,
			charge:   79,
			wantCode: http.StatusBadRequest,
			wantBody: "already at 79%",
		},
		{
			name:       "legacy one percent below the limit",
			fw:         firmwareLegacy,
			path:       chargeOnceLimitPath,
			charge:     79,
			wantCode:   http.StatusCreated,
			wantTarget: 80,
		},
		{
			// A one-time charge to 100% deactivates the limit instead of
			// narrowing a band, so it has no percent to give away.
			name:       "firmware one percent below full",
			fw:         firmwareManaged,
			path:       chargeOnceFullPath,
			charge:     99,
			wantCode:   http.StatusCreated,
			wantTarget: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := chargeOnceConf(80, 78)
			// An admitted one-time charge runs the maintain loop right away, so
			// the backend under test needs its simulated firmware.
			f := newFixture(t, tt.fw, withConfig(conf), withCharge(tt.charge), withCharging(false))

			response := f.post(tt.path)
			if response.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, tt.wantCode, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), tt.wantBody) {
				t.Fatalf("response does not explain the outcome: %s", response.Body.String())
			}
			if conf.ChargeOnceTarget() != tt.wantTarget {
				t.Fatalf("chargeOnceTarget = %d, want %d", conf.ChargeOnceTarget(), tt.wantTarget)
			}
		})
	}
}
