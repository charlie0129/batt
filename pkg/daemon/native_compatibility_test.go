package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charlie0129/batt/pkg/compatibility"
)

func useExactNativeTargets(t *testing.T) *fakeNativeLimit {
	t.Helper()
	gatedSMCNoAdapter(t)
	useNativeCapabilities(t)
	capabilities.NativeExactTargets = true
	fake := &fakeNativeLimit{limit: 100, exact: true}
	useFakeNativeLimit(t, fake)
	return fake
}

func TestNativeChargeNowUsesExistingRoute(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	file.SetLowerLimit(50)
	fake := useExactNativeTargets(t)
	stubAdapter(t, true)
	stubBatteryCharge(t, 52)
	if !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("configured target not applied")
	}
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/charge-once/limit", nil))
	if response.Code != http.StatusCreated || file.ChargeOnceTarget() != 55 || fake.limit != 55 {
		t.Fatalf("existing charge-now action did not request 55%%: %d %s", response.Code, response.Body.String())
	}
	if file.UpperLimit() != 55 || file.LowerLimit() != 50 {
		t.Fatal("one-time charge changed the configured limits")
	}
	disableUnsupportedConfiguredFeatures()
	if file.ChargeOnceTarget() != 55 || !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("restart recovery discarded a supported one-time charge")
	}
	stubBatteryCharge(t, 55)
	if !completeChargeOnce(file) || !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("completion did not retain the configured target")
	}
	stubBatteryCharge(t, 54)
	if !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("completion resumed following the battery percentage")
	}
}

func TestNativeChargeNowFailurePreservesTarget(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	fake := useExactNativeTargets(t)
	stubAdapter(t, true)
	stubBatteryCharge(t, 52)
	if !maintainLoopForced() {
		t.Fatal("initial target failed")
	}
	fake.err = errors.New("native request failed")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/charge-once/limit", nil))
	if response.Code != http.StatusInternalServerError || file.ChargeOnceTarget() != 0 || !strings.Contains(response.Body.String(), "failed to enforce one-time charge") {
		t.Fatalf("failed one-time action was accepted or persisted: %d %s", response.Code, response.Body.String())
	}
	fake.err = nil
	if !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("failed one-time request lost the configured target")
	}
}

func TestNativeFullChargeRestoresTargetAt100Percent(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	fake := useExactNativeTargets(t)
	stubAdapter(t, true)
	stubBatteryCharge(t, 52)
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/charge-once/full", nil))
	if response.Code != http.StatusCreated || file.ChargeOnceTarget() != 100 || fake.enabled {
		t.Fatalf("full charge failed: %d %s", response.Code, response.Body.String())
	}
	stubBatteryCharge(t, 100)
	if !completeChargeOnce(file) || !maintainLoopForced() || !fake.enabled || fake.limit != 55 {
		t.Fatal("reaching 100% did not restore the configured target")
	}
}

func TestEarlierNativeBackendKeepsPreviousPaths(t *testing.T) {
	file, _ := useTempConfig(t)
	gatedSMCNoAdapter(t)
	useNativeCapabilities(t, 80, 85, 90, 95, 100)
	fake := &fakeNativeLimit{limit: 100}
	useFakeNativeLimit(t, fake)
	file.SetUpperLimit(80)
	if !maintainLoopForced() || fake.limit != 80 {
		t.Fatal("earlier native backend did not use its slider target")
	}
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/lower-limit-delta", strings.NewReader("5")))
	if response.Code != http.StatusConflict {
		t.Fatal("earlier native backend gained an unsupported lower-limit command")
	}
	file.SetUpperLimit(100)
	fake.limit, fake.enabled, fake.registered = 100, false, true
	if _, err := ensureNativeChargeLimitDisabled(); err != nil || fake.disables != 0 {
		t.Fatal("earlier backend entered the macOS 27 registration cleanup path")
	}
}

func TestNativeFeatureCompatibility(t *testing.T) {
	caps := compatibility.Capabilities{
		ChargingControl: true, ChargeControlMode: compatibility.ChargeControlNative,
		NativeExactTargets: true, AdapterControl: true, Calibration: true,
	}
	for feature, want := range map[compatibility.Feature]bool{
		compatibility.FeatureChargingControl: true, compatibility.FeatureLowerLimit: false,
		compatibility.FeatureAdapterControl: true, compatibility.FeatureCalibration: true,
		compatibility.FeatureSleepHooks: false, compatibility.FeatureMagSafeLED: false,
	} {
		if got := caps.Supports(feature); got != want {
			t.Errorf("%s supported=%t want=%t", feature, got, want)
		}
	}
}
