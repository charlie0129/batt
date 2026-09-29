package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charlie0129/gosmc"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

// fakeNativeLimit stands in for PowerUIAgent.
type fakeNativeLimit struct {
	supported bool
	limits    []int
	limit     int
	enabled   bool
	err       error
	setCalls  []int
	disables  int
}

func (f *fakeNativeLimit) Supported() bool { return f.supported }

func (f *fakeNativeLimit) AvailableLimits() ([]int, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]int(nil), f.limits...), nil
}

func (f *fakeNativeLimit) Limit() (int, bool, error) {
	if f.err != nil {
		return 0, false, f.err
	}
	return f.limit, f.enabled, nil
}

func (f *fakeNativeLimit) SetLimit(limit int) error {
	if f.err != nil {
		return f.err
	}
	f.setCalls = append(f.setCalls, limit)
	f.limit = limit
	f.enabled = limit < 100
	return nil
}

func (f *fakeNativeLimit) Disable() error {
	if f.err != nil {
		return f.err
	}
	f.disables++
	f.limit = 100
	f.enabled = false
	return nil
}

func useFakeNativeLimit(t *testing.T, fake *fakeNativeLimit) {
	t.Helper()
	previous := nativeLimit
	t.Cleanup(func() { nativeLimit = previous })
	nativeLimit = fake
}

func useNativeCapabilities(t *testing.T, limits ...int) {
	t.Helper()
	previous := capabilities
	t.Cleanup(func() { capabilities = previous })
	capabilities = compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlNative,
		SupportedLimits:   limits,
		AdapterControl:    true,
		Calibration:       true,
	}
}

func useTempConfig(t *testing.T) (*config.File, string) {
	t.Helper()
	path := t.TempDir() + "/batt.json"
	file, err := config.NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previous := conf
	t.Cleanup(func() { conf = previous })
	conf = file
	return file, path
}

// gatedSMC mimics macOS 27 beta 4+ firmware: adapter control works, but no
// charge-control key is usable.
func gatedSMC(t *testing.T) *smc.AppleSMC {
	t.Helper()
	adapter, err := gosmc.NewValue(smc.AdapterKey3, gosmc.TypeUInt8, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	return useMockSMC(t, adapter)
}

// gatedSMCNoAdapter mimics firmware where neither the charge keys nor the
// adapter key is usable, so only the native macOS limit could remain.
func gatedSMCNoAdapter(t *testing.T) *smc.AppleSMC {
	t.Helper()
	return useMockSMC(t)
}

func useMockSMC(t *testing.T, values ...gosmc.Value) *smc.AppleSMC {
	t.Helper()
	mock := smc.NewMockValues(values...)
	if err := mock.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mock.Close() })
	previous := smcConn
	t.Cleanup(func() { smcConn = previous })
	smcConn = mock
	return mock
}

func TestDetectCapabilitiesPrefersAdapterWhenOptedIn(t *testing.T) {
	gatedSMC(t)
	file, _ := useTempConfig(t)
	file.SetAdapterMode(true)
	useFakeNativeLimit(t, &fakeNativeLimit{supported: true, limits: []int{80, 90, 100}})

	got := detectCapabilities()
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

func TestDetectCapabilitiesRequiresAdapterOptIn(t *testing.T) {
	gatedSMC(t)
	useTempConfig(t) // AdapterMode defaults to false
	useFakeNativeLimit(t, &fakeNativeLimit{supported: true, limits: []int{80, 100}})

	got := detectCapabilities()
	if got.ChargeControlMode != compatibility.ChargeControlNative {
		t.Fatalf("without opt-in, adapter must not be selected; got %s", got.ChargeControlMode)
	}
}

func TestDetectCapabilitiesFallsBackToNativeLimit(t *testing.T) {
	gatedSMCNoAdapter(t)
	useTempConfig(t)
	useFakeNativeLimit(t, &fakeNativeLimit{supported: true, limits: []int{100, 80, 90}})

	got := detectCapabilities()
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

func TestDetectCapabilitiesStaysUnsupportedWithoutNativeLimit(t *testing.T) {
	useTempConfig(t)
	for name, fake := range map[string]*fakeNativeLimit{
		"not supported": {supported: false},
		"limits error":  {supported: true, err: errors.New("xpc down")},
		"no limits":     {supported: true},
	} {
		gatedSMCNoAdapter(t)
		useFakeNativeLimit(t, fake)
		got := detectCapabilities()
		if got.ChargeControlMode != compatibility.ChargeControlUnsupported || got.ChargingControl || got.Calibration {
			t.Errorf("%s: capabilities = %+v, want unsupported", name, got)
		}
	}
}

func TestDisableUnsupportedConfiguredFeaturesRaisesLimit(t *testing.T) {
	file, path := useTempConfig(t)
	file.SetUpperLimit(60)
	useNativeCapabilities(t, 80, 85, 90, 95, 100)

	disableUnsupportedConfiguredFeatures()

	if file.UpperLimit() != 80 || file.LowerLimit() != 78 {
		t.Fatalf("limits = %d/%d, want 80/78", file.UpperLimit(), file.LowerLimit())
	}
	reloaded, err := config.NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.UpperLimit() != 80 {
		t.Fatalf("raised limit was not persisted, got %d", reloaded.UpperLimit())
	}

	// Supported and disabled limits are left alone.
	file.SetUpperLimit(85)
	disableUnsupportedConfiguredFeatures()
	if file.UpperLimit() != 85 {
		t.Fatalf("supported limit changed to %d", file.UpperLimit())
	}
	file.SetUpperLimit(100)
	disableUnsupportedConfiguredFeatures()
	if file.UpperLimit() != 100 {
		t.Fatalf("disabled limit changed to %d", file.UpperLimit())
	}
}

func TestEnsureNativeChargeLimit(t *testing.T) {
	useNativeCapabilities(t, 80, 85, 90, 95, 100)

	fake := &fakeNativeLimit{limit: 100}
	useFakeNativeLimit(t, fake)
	changed, err := ensureNativeChargeLimit(80)
	if err != nil || !changed || fake.limit != 80 || !fake.enabled {
		t.Fatalf("first apply: changed=%v err=%v fake=%+v", changed, err, fake)
	}

	changed, err = ensureNativeChargeLimit(80)
	if err != nil || changed || len(fake.setCalls) != 1 {
		t.Fatalf("matching limit must not be rewritten: changed=%v err=%v calls=%v", changed, err, fake.setCalls)
	}

	// The user changed the limit in System Settings; batt takes it back.
	fake.limit = 90
	changed, err = ensureNativeChargeLimit(80)
	if err != nil || !changed || fake.limit != 80 {
		t.Fatalf("drift was not corrected: changed=%v err=%v fake=%+v", changed, err, fake)
	}

	// Unsupported values are raised, never lowered.
	changed, err = ensureNativeChargeLimit(81)
	if err != nil || !changed || fake.limit != 85 {
		t.Fatalf("81 should apply 85: changed=%v err=%v fake=%+v", changed, err, fake)
	}

	// 100 disables the limit and is idempotent.
	changed, err = ensureNativeChargeLimit(100)
	if err != nil || !changed || fake.enabled || fake.disables != 1 {
		t.Fatalf("100 should disable: changed=%v err=%v fake=%+v", changed, err, fake)
	}
	changed, err = ensureNativeChargeLimit(100)
	if err != nil || changed || fake.disables != 1 {
		t.Fatalf("disable must be idempotent: changed=%v err=%v fake=%+v", changed, err, fake)
	}

	fake.err = errors.New("xpc down")
	if _, err := ensureNativeChargeLimit(80); err == nil {
		t.Fatal("errors from PowerUIAgent must be reported")
	}
}

func TestMaintainManagedChargeLimitNative(t *testing.T) {
	file, _ := useTempConfig(t)
	useNativeCapabilities(t, 80, 85, 90, 95, 100)
	fake := &fakeNativeLimit{limit: 100}
	useFakeNativeLimit(t, fake)

	file.SetUpperLimit(90)
	if !maintainManagedChargeLimit() || fake.limit != 90 || !fake.enabled {
		t.Fatalf("limit was not applied: %+v", fake)
	}

	file.SetUpperLimit(100)
	if !maintainManagedChargeLimit() || fake.enabled {
		t.Fatalf("limit was not disabled: %+v", fake)
	}
}

func TestAdapterModeTransitionDisablesNativeLimit(t *testing.T) {
	adapterMockSMC(t, 60, true, true)
	file, _ := useTempConfig(t)
	fake := &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}, limit: 80, enabled: true}
	useFakeNativeLimit(t, fake)
	useNativeCapabilities(t, 80, 85, 90, 95, 100)
	previousCharger := charger
	t.Cleanup(func() { charger = previousCharger })

	request := httptest.NewRequest(http.MethodPut, "/adapter-mode", strings.NewReader("true"))
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || fake.enabled || !file.AdapterMode() || capabilities.ChargeControlMode != compatibility.ChargeControlAdapter {
		t.Fatalf("entering adapter mode must clear native limit: status=%d, native=%+v, mode=%s", response.Code, fake, capabilities.ChargeControlMode)
	}
}

func TestAdapterModeReconciliationFailureRestoresNativeMode(t *testing.T) {
	gatedSMC(t) // adapter key exists, but the missing battery key makes enforcement fail
	file, path := useTempConfig(t)
	fake := &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}, limit: 80, enabled: true}
	useFakeNativeLimit(t, fake)
	useNativeCapabilities(t, 80, 85, 90, 95, 100)
	previousCharger := charger
	t.Cleanup(func() { charger = previousCharger })

	request := httptest.NewRequest(http.MethodPut, "/adapter-mode", strings.NewReader("true"))
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || file.AdapterMode() || capabilities.ChargeControlMode != compatibility.ChargeControlNative || !fake.enabled || fake.limit != 80 {
		t.Fatalf("failed adapter backend must restore native 80%% limit: status=%d, native=%+v, mode=%s", response.Code, fake, capabilities.ChargeControlMode)
	}
	reloaded, err := config.NewFile(path)
	if err != nil || reloaded.AdapterMode() {
		t.Fatalf("adapter-mode setting must roll back on disk: config=%v, err=%v", reloaded, err)
	}
}

func TestAdapterModeTransitionFailureRollsBack(t *testing.T) {
	gatedSMC(t)
	file, path := useTempConfig(t)
	fake := &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}, limit: 80, enabled: true, err: errors.New("PowerUI unavailable")}
	useFakeNativeLimit(t, fake)
	useNativeCapabilities(t, 80, 85, 90, 95, 100)
	previousCharger := charger
	t.Cleanup(func() { charger = previousCharger })

	request := httptest.NewRequest(http.MethodPut, "/adapter-mode", strings.NewReader("true"))
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || file.AdapterMode() || capabilities.ChargeControlMode != compatibility.ChargeControlNative {
		t.Fatalf("failed native reset must keep native mode: status=%d, mode=%s", response.Code, capabilities.ChargeControlMode)
	}
	reloaded, err := config.NewFile(path)
	if err != nil || reloaded.AdapterMode() {
		t.Fatalf("adapter-mode setting must roll back on disk: config=%v, err=%v", reloaded, err)
	}
}

func TestLeavingAdapterModeRequiresWallPowerRestore(t *testing.T) {
	gatedSMCNoAdapter(t) // missing adapter key makes EnableAdapter fail
	file, path := useTempConfig(t)
	file.SetAdapterMode(true)
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}
	fake := &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}, limit: 80, enabled: false}
	useFakeNativeLimit(t, fake)
	previousCap, previousCharger := capabilities, charger
	t.Cleanup(func() { capabilities, charger = previousCap, previousCharger })
	capabilities = compatibility.Capabilities{ChargingControl: true, ChargeControlMode: compatibility.ChargeControlAdapter}
	charger = adapterSwitch{}

	request := httptest.NewRequest(http.MethodPut, "/adapter-mode", strings.NewReader("false"))
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !file.AdapterMode() || capabilities.ChargeControlMode != compatibility.ChargeControlAdapter {
		t.Fatalf("failed adapter restore must not change mode: status=%d, mode=%s", response.Code, capabilities.ChargeControlMode)
	}
	reloaded, err := config.NewFile(path)
	if err != nil || !reloaded.AdapterMode() {
		t.Fatalf("adapter-mode setting must roll back on disk: config=%v, err=%v", reloaded, err)
	}
}

func TestLeavingAdapterModeCancelsUnsupportedOneTimeTarget(t *testing.T) {
	gatedSMC(t)
	file, path := useTempConfig(t)
	file.SetAdapterMode(true)
	file.SetChargeOnceTarget(80)
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}
	fake := &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}, limit: 80, enabled: true}
	useFakeNativeLimit(t, fake)
	previousCap, previousCharger := capabilities, charger
	t.Cleanup(func() { capabilities, charger = previousCap, previousCharger })
	capabilities = compatibility.Capabilities{ChargingControl: true, ChargeControlMode: compatibility.ChargeControlAdapter}
	charger = adapterSwitch{}

	request := httptest.NewRequest(http.MethodPut, "/adapter-mode", strings.NewReader("false"))
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || file.AdapterMode() || file.ChargeOnceTarget() != 0 || capabilities.ChargeControlMode != compatibility.ChargeControlNative {
		t.Fatalf("native transition must drop unsupported target: status=%d, mode=%s, target=%d", response.Code, capabilities.ChargeControlMode, file.ChargeOnceTarget())
	}
	reloaded, err := config.NewFile(path)
	if err != nil || reloaded.ChargeOnceTarget() != 0 {
		t.Fatalf("unsupported target must be cleared on disk: config=%v, err=%v", reloaded, err)
	}
}

func TestResetChargeControlNative(t *testing.T) {
	useNativeCapabilities(t, 80, 100)
	fake := &fakeNativeLimit{limit: 80, enabled: true}
	useFakeNativeLimit(t, fake)

	if err := resetChargeControl(); err != nil || fake.enabled {
		t.Fatalf("reset: err=%v fake=%+v", err, fake)
	}
}

func TestSetLimitRejectsUnsupportedNativeLimit(t *testing.T) {
	useTempConfig(t)
	useNativeCapabilities(t, 80, 85, 90, 95, 100)
	useFakeNativeLimit(t, &fakeNativeLimit{limit: 100})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/limit", strings.NewReader("70"))
	setupRoutes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "80%, 85%, 90%, 95% or 100%") {
		t.Fatalf("error must list the supported limits, got %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPut, "/lower-limit-delta", strings.NewReader("5"))
	setupRoutes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("lower-limit-delta status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
}
