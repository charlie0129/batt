package daemon

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/charlie0129/batt/pkg/compatibility"
)

// newExactNativeFixture is a macOS 27 Mac whose charge keys are gated, whose
// adapter key works, and whose PowerUIAgent accepts exact targets. It uses a
// real config file.
func newExactNativeFixture(t *testing.T, opts ...fixtureOption) *fixture {
	t.Helper()
	opts = append([]fixtureOption{withFileConfig(), withNative(exactNative())}, opts...)
	f := newFixture(t, firmwareGated, opts...)
	if caps := f.caps(); !caps.NativeExactTargets || caps.ChargeControlMode != compatibility.ChargeControlNative {
		t.Fatalf("exact native capabilities not detected: %+v", caps)
	}
	return f
}

func TestDetectCapabilitiesPrefersAdapterWhenOptedIn(t *testing.T) {
	f := newFixture(t, firmwareGated, withFileConfig(), withNative(sliderNative(80, 90, 100)))
	f.file.SetAdapterMode(true)

	got := f.d.detectCapabilities()
	if got.ChargeControlMode != compatibility.ChargeControlAdapter || !got.ChargingControl {
		t.Fatalf("mode = %+v, want adapter", got)
	}
	if len(got.SupportedLimits) != 0 {
		t.Fatalf("adapter mode must accept any limit, got SupportedLimits=%v", got.SupportedLimits)
	}
	if !got.SleepHooks {
		t.Fatal("adapter mode needs sleep hooks")
	}
	if got.MagSafeLED || got.AdapterControl || got.Calibration {
		t.Fatalf("adapter mode must hide MagSafe/manual-adapter/calibration: %+v", got)
	}
	if !got.Supports(compatibility.FeatureLowerLimit) {
		t.Fatal("adapter mode uses a lower-limit hysteresis band")
	}
	if !got.SupportsLimit(60) {
		t.Fatal("adapter mode must accept sub-80 limits")
	}
}

func TestDetectCapabilitiesAdapterModeOffersMagSafeLED(t *testing.T) {
	f := newFixture(t, firmwareGated.withMagSafe(true), withFileConfig(), withNative(sliderNative(80, 90, 100)))
	f.file.SetAdapterMode(true)

	got := f.d.detectCapabilities()
	if got.ChargeControlMode != compatibility.ChargeControlAdapter {
		t.Fatalf("mode = %+v, want adapter", got)
	}
	if !got.MagSafeLED {
		t.Fatal("adapter mode owns the wall-power switch, so it can drive the MagSafe LED")
	}
}

func TestDetectCapabilitiesRequiresAdapterOptIn(t *testing.T) {
	// AdapterMode defaults to false.
	f := newFixture(t, firmwareGated, withFileConfig(), withNative(sliderNative(80, 100)))

	got := f.d.detectCapabilities()
	if got.ChargeControlMode != compatibility.ChargeControlNative {
		t.Fatalf("without opt-in, adapter must not be selected; got %s", got.ChargeControlMode)
	}
}

func TestDetectCapabilitiesFallsBackToNativeLimit(t *testing.T) {
	f := newFixture(t, firmwareGatedNoAdapter, withFileConfig(), withNative(sliderNative(100, 80, 90)))

	got := f.d.detectCapabilities()
	if got.ChargeControlMode != compatibility.ChargeControlNative || !got.ChargingControl {
		t.Fatalf("mode = %+v, want native", got)
	}
	if len(got.SupportedLimits) != 3 {
		t.Fatalf("supported limits = %v", got.SupportedLimits)
	}
	if got.SleepHooks || got.MagSafeLED || got.AdapterControl || got.Calibration {
		t.Fatalf("unexpected native capabilities: %+v", got)
	}
	if got.Supports(compatibility.FeatureLowerLimit) {
		t.Fatal("native mode must not advertise a lower limit")
	}
}

func TestDetectCapabilitiesNativeModeOffersMagSafeLED(t *testing.T) {
	f := newFixture(t, firmwareGatedNoAdapter.withMagSafe(true), withFileConfig(), withNative(sliderNative(80, 100)))

	got := f.d.detectCapabilities()
	if got.ChargeControlMode != compatibility.ChargeControlNative || !got.MagSafeLED {
		t.Fatalf("native mode must offer MagSafe LED control: %+v", got)
	}
}

func TestDetectCapabilitiesStaysUnsupportedWithoutNativeLimit(t *testing.T) {
	for name, fake := range map[string]*fakeNativeLimit{
		"not supported": {supported: false},
		"limits error":  {supported: true, err: errors.New("xpc down")},
		"no limits":     {supported: true},
	} {
		f := newFixture(t, firmwareGatedNoAdapter, withFileConfig(), withNative(fake))
		got := f.d.detectCapabilities()
		if got.ChargeControlMode != compatibility.ChargeControlUnsupported || got.ChargingControl || got.Calibration {
			t.Errorf("%s: capabilities = %+v, want unsupported", name, got)
		}
	}
}

func TestDetectCapabilitiesOffersExactNativeTargets(t *testing.T) {
	f := newFixture(t, firmwareGatedNoAdapter, withFileConfig(),
		withNative(&fakeNativeLimit{supported: true, exact: true, limits: []int{50, 55, 100}}))
	got := f.d.detectCapabilities()
	if !got.NativeExactTargets || !got.Supports(compatibility.FeatureLowerLimit) || got.SleepHooks {
		t.Fatalf("incorrect exact-native capabilities: %+v", got)
	}
}

func TestDisableUnsupportedConfiguredFeaturesRaisesLimit(t *testing.T) {
	f := newFixture(t, firmwareGatedNoAdapter, withFileConfig(), withCapabilities(nativeCaps(80, 85, 90, 95, 100)))
	f.file.SetUpperLimit(60)

	f.d.disableUnsupportedConfiguredFeatures()

	if f.file.UpperLimit() != 80 || f.file.LowerLimit() != 78 {
		t.Fatalf("limits = %d/%d, want 80/78", f.file.UpperLimit(), f.file.LowerLimit())
	}
	if reloaded := f.reloadFile(); reloaded.UpperLimit() != 80 {
		t.Fatalf("raised limit was not persisted, got %d", reloaded.UpperLimit())
	}

	// Supported and disabled limits are left alone.
	f.file.SetUpperLimit(85)
	f.d.disableUnsupportedConfiguredFeatures()
	if f.file.UpperLimit() != 85 {
		t.Fatalf("supported limit changed to %d", f.file.UpperLimit())
	}
	f.file.SetUpperLimit(100)
	f.d.disableUnsupportedConfiguredFeatures()
	if f.file.UpperLimit() != 100 {
		t.Fatalf("disabled limit changed to %d", f.file.UpperLimit())
	}
}

func TestEnsureNativeChargeLimit(t *testing.T) {
	fake := &fakeNativeLimit{limit: 100}
	f := newFixture(t, firmwareGatedNoAdapter, withNative(fake), withCapabilities(nativeCaps(80, 85, 90, 95, 100)))

	changed, err := f.d.ensureNativeChargeLimit(80)
	if err != nil || !changed || fake.limit != 80 || !fake.enabled {
		t.Fatalf("first apply: changed=%v err=%v fake=%+v", changed, err, fake)
	}

	changed, err = f.d.ensureNativeChargeLimit(80)
	if err != nil || changed || len(fake.setCalls) != 1 {
		t.Fatalf("matching limit must not be rewritten: changed=%v err=%v calls=%v", changed, err, fake.setCalls)
	}

	// The user changed the limit in System Settings; batt takes it back.
	fake.limit = 90
	changed, err = f.d.ensureNativeChargeLimit(80)
	if err != nil || !changed || fake.limit != 80 {
		t.Fatalf("drift was not corrected: changed=%v err=%v fake=%+v", changed, err, fake)
	}

	// Unsupported values are raised, never lowered.
	changed, err = f.d.ensureNativeChargeLimit(81)
	if err != nil || !changed || fake.limit != 85 {
		t.Fatalf("81 should apply 85: changed=%v err=%v fake=%+v", changed, err, fake)
	}

	// 100 disables the limit and is idempotent.
	changed, err = f.d.ensureNativeChargeLimit(100)
	if err != nil || !changed || fake.enabled || fake.disables != 1 {
		t.Fatalf("100 should disable: changed=%v err=%v fake=%+v", changed, err, fake)
	}
	changed, err = f.d.ensureNativeChargeLimit(100)
	if err != nil || changed || fake.disables != 1 {
		t.Fatalf("disable must be idempotent: changed=%v err=%v fake=%+v", changed, err, fake)
	}

	fake.err = errors.New("xpc down")
	if _, err := f.d.ensureNativeChargeLimit(80); err == nil {
		t.Fatal("errors from PowerUIAgent must be reported")
	}
}

func TestMaintainManagedChargeLimitNative(t *testing.T) {
	fake := &fakeNativeLimit{limit: 100}
	f := newFixture(t, firmwareGatedNoAdapter, withFileConfig(), withNative(fake), withCapabilities(nativeCaps(80, 85, 90, 95, 100)))

	f.file.SetUpperLimit(90)
	if !f.d.maintainManagedChargeLimit() || fake.limit != 90 || !fake.enabled {
		t.Fatalf("limit was not applied: %+v", fake)
	}

	f.file.SetUpperLimit(100)
	if !f.d.maintainManagedChargeLimit() || fake.enabled {
		t.Fatalf("limit was not disabled: %+v", fake)
	}
}

func TestAdapterModeTransitionDisablesNativeLimit(t *testing.T) {
	fake := &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}, limit: 80, enabled: true}
	f := newFixture(t, firmwareGated, withFileConfig(), withCharge(60), withNative(fake),
		withCapabilities(nativeCaps(80, 85, 90, 95, 100)))

	response := f.put("/adapter-mode", "true")
	if response.Code != http.StatusCreated || fake.enabled || !f.file.AdapterMode() || f.caps().ChargeControlMode != compatibility.ChargeControlAdapter {
		t.Fatalf("entering adapter mode must clear native limit: status=%d, native=%+v, mode=%s", response.Code, fake, f.caps().ChargeControlMode)
	}
}

func TestAdapterModeReconciliationFailureRestoresNativeMode(t *testing.T) {
	// The adapter key works, but the adapter loop cannot read the battery
	// charge, so enforcing the new mode fails.
	fake := &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}, limit: 80, enabled: true}
	f := newFixture(t, firmwareGated, withFileConfig(), withNative(fake),
		withCapabilities(nativeCaps(80, 85, 90, 95, 100)))
	f.smc.fail("GetBatteryCharge", errors.New("battery key gated"))

	response := f.put("/adapter-mode", "true")
	if response.Code != http.StatusInternalServerError || f.file.AdapterMode() || f.caps().ChargeControlMode != compatibility.ChargeControlNative || !fake.enabled || fake.limit != 80 {
		t.Fatalf("failed adapter backend must restore native 80%% limit: status=%d, native=%+v, mode=%s", response.Code, fake, f.caps().ChargeControlMode)
	}
	if reloaded := f.reloadFile(); reloaded.AdapterMode() {
		t.Fatal("adapter-mode setting must roll back on disk")
	}
}

func TestAdapterModeTransitionFailureRollsBack(t *testing.T) {
	fake := &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}, limit: 80, enabled: true, err: errors.New("PowerUI unavailable")}
	f := newFixture(t, firmwareGated, withFileConfig(), withNative(fake),
		withCapabilities(nativeCaps(80, 85, 90, 95, 100)))

	response := f.put("/adapter-mode", "true")
	if response.Code != http.StatusInternalServerError || f.file.AdapterMode() || f.caps().ChargeControlMode != compatibility.ChargeControlNative {
		t.Fatalf("failed native reset must keep native mode: status=%d, mode=%s", response.Code, f.caps().ChargeControlMode)
	}
	if reloaded := f.reloadFile(); reloaded.AdapterMode() {
		t.Fatal("adapter-mode setting must roll back on disk")
	}
}

func TestLeavingAdapterModeRequiresWallPowerRestore(t *testing.T) {
	// Without an adapter key, wall power cannot be restored.
	fake := &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}, limit: 80, enabled: false}
	f := newFixture(t, firmwareGatedNoAdapter, withFileConfig(), withNative(fake),
		withCapabilities(compatibility.Capabilities{ChargingControl: true, ChargeControlMode: compatibility.ChargeControlAdapter}))
	f.file.SetAdapterMode(true)
	if err := f.file.Save(); err != nil {
		t.Fatal(err)
	}

	response := f.put("/adapter-mode", "false")
	if response.Code != http.StatusInternalServerError || !f.file.AdapterMode() || f.caps().ChargeControlMode != compatibility.ChargeControlAdapter {
		t.Fatalf("failed adapter restore must not change mode: status=%d, mode=%s", response.Code, f.caps().ChargeControlMode)
	}
	if reloaded := f.reloadFile(); !reloaded.AdapterMode() {
		t.Fatal("adapter-mode setting must roll back on disk")
	}
}

func TestLeavingAdapterModeCancelsUnsupportedOneTimeTarget(t *testing.T) {
	fake := &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}, limit: 80, enabled: true}
	f := newFixture(t, firmwareGated, withFileConfig(), withNative(fake),
		withCapabilities(compatibility.Capabilities{ChargingControl: true, ChargeControlMode: compatibility.ChargeControlAdapter}))
	f.file.SetAdapterMode(true)
	f.file.SetChargeOnceTarget(80)
	if err := f.file.Save(); err != nil {
		t.Fatal(err)
	}

	response := f.put("/adapter-mode", "false")
	if response.Code != http.StatusCreated || f.file.AdapterMode() || f.file.ChargeOnceTarget() != 0 || f.caps().ChargeControlMode != compatibility.ChargeControlNative {
		t.Fatalf("native transition must drop unsupported target: status=%d, mode=%s, target=%d", response.Code, f.caps().ChargeControlMode, f.file.ChargeOnceTarget())
	}
	if reloaded := f.reloadFile(); reloaded.ChargeOnceTarget() != 0 {
		t.Fatal("unsupported target must be cleared on disk")
	}
}

func TestResetChargeControlNative(t *testing.T) {
	fake := &fakeNativeLimit{limit: 80, enabled: true}
	f := newFixture(t, firmwareGatedNoAdapter, withNative(fake), withCapabilities(nativeCaps(80, 100)))

	if err := f.d.resetChargeControl(); err != nil || fake.enabled {
		t.Fatalf("reset: err=%v fake=%+v", err, fake)
	}
}

func TestSetLimitRejectsUnsupportedNativeLimit(t *testing.T) {
	f := newFixture(t, firmwareGatedNoAdapter, withFileConfig(), withNative(&fakeNativeLimit{limit: 100}),
		withCapabilities(nativeCaps(80, 85, 90, 95, 100)))

	recorder := f.put("/limit", "70")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "80%, 85%, 90%, 95% or 100%") {
		t.Fatalf("error must list the supported limits, got %s", recorder.Body.String())
	}

	recorder = f.put("/lower-limit-delta", "5")
	if recorder.Code != http.StatusConflict {
		t.Fatalf("lower-limit-delta status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
}

func TestNativeExactLimitsAreAppliedWithoutAdapterControl(t *testing.T) {
	for _, target := range []int{10, 55, 60, 69, 79, 81, 99} {
		t.Run(strconv.Itoa(target), func(t *testing.T) {
			f := newExactNativeFixture(t)
			response := f.put("/limit", strconv.Itoa(target))
			if response.Code != http.StatusCreated || f.native.limit != target || !f.native.enabled {
				t.Fatalf("target %d: status=%d body=%s native=%+v", target, response.Code, response.Body.String(), f.native)
			}
			f.d.disableUnsupportedConfiguredFeatures()
			if reloaded := f.reloadFile(); reloaded.UpperLimit() != target || f.file.AdapterMode() {
				t.Fatalf("exact limit not retained or adapter selected: config=%+v", reloaded)
			}
			if !f.d.maintainManagedChargeLimit() || len(f.native.setCalls) != 1 {
				t.Fatal("unchanged target rewritten")
			}
		})
	}
}

func TestSetNativeLimitReportsApplicationFailure(t *testing.T) {
	f := newExactNativeFixture(t)
	f.native.err = errors.New("agent unavailable")
	response := f.put("/limit", "69")
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "applying it failed") {
		t.Fatalf("failed apply reported as success: %d %s", response.Code, response.Body.String())
	}
	if f.file.UpperLimit() != 69 {
		t.Fatal("saved target lost; daemon must be able to retry it")
	}
}
