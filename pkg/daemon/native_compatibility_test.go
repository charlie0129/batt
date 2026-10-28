package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charlie0129/batt/pkg/compatibility"
)

func TestNativeBandChargeNowUsesExistingRoute(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	file.SetLowerLimit(50)
	fake, battery := useNativeBand(t, nativeBatteryState{charge: 52, plugged: true})
	stubAdapter(t, true)
	// Simulate the API's SMC percentage separately from the read-only IOKit
	// snapshot used by band control. No hardware is read or written.
	stubBatteryCharge(t, 52)
	if !maintainLoopForced() || fake.limit != 52 {
		t.Fatal("initial paused state not established")
	}
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/charge-once/limit", nil))
	if response.Code != http.StatusCreated || file.ChargeOnceTarget() != 55 || fake.limit != 55 {
		t.Fatalf("existing charge-now action did not request 55%%: %d %s", response.Code, response.Body.String())
	}
	if file.UpperLimit() != 55 || file.LowerLimit() != 50 {
		t.Fatal("one-time charge changed the configured band")
	}
	disableUnsupportedConfiguredFeatures()
	if file.ChargeOnceTarget() != 55 {
		t.Fatal("restart recovery discarded a supported one-time charge")
	}
	resetNativeBand()
	if !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("persisted one-time charge did not survive band-state restart")
	}
	battery.charge = 55
	stubBatteryCharge(t, 55)
	if !completeChargeOnce(file) || !maintainLoopForced() || fake.limit != 55 {
		t.Fatal("completion restored a stale pause target instead of the upper limit")
	}
	battery.charge = 54
	if !maintainLoopForced() || fake.limit != 54 {
		t.Fatal("completion did not return to the configured band")
	}
}

func TestNativeBandChargeNowFailurePreservesPausedIntent(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	file.SetLowerLimit(50)
	fake, _ := useNativeBand(t, nativeBatteryState{charge: 52, plugged: true})
	stubAdapter(t, true)
	stubBatteryCharge(t, 52)
	if !maintainLoopForced() {
		t.Fatal("initial hold failed")
	}
	fake.err = errors.New("native request failed")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/charge-once/limit", nil))
	if response.Code != http.StatusInternalServerError || file.ChargeOnceTarget() != 0 || !strings.Contains(response.Body.String(), "failed to enforce one-time charge") {
		t.Fatalf("failed one-time action was accepted or persisted: %d %s", response.Code, response.Body.String())
	}
	fake.err = nil
	if !maintainLoopForced() || fake.limit != 52 {
		t.Fatal("failed one-time request lost the previous paused intent")
	}
}

func TestEarlierNativeBackendKeepsPreviousPaths(t *testing.T) {
	file, _ := useTempConfig(t)
	useNativeCapabilities(t, 80, 85, 90, 95, 100)
	fake := &fakeNativeLimit{limit: 100}
	useFakeNativeLimit(t, fake)
	previousRead := readNativeBatteryState
	t.Cleanup(func() { readNativeBatteryState = previousRead })
	readNativeBatteryState = func() (nativeBatteryState, error) {
		t.Fatal("earlier native backend entered macOS 27 band control")
		return nativeBatteryState{}, nil
	}
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

func TestNativeBandFeatureCompatibility(t *testing.T) {
	caps := compatibility.Capabilities{
		ChargingControl: true, ChargeControlMode: compatibility.ChargeControlNative,
		NativeChargeBand: true, AdapterControl: true, Calibration: true,
	}
	for feature, want := range map[compatibility.Feature]bool{
		compatibility.FeatureChargingControl: true, compatibility.FeatureLowerLimit: true,
		compatibility.FeatureAdapterControl: true, compatibility.FeatureCalibration: true,
		compatibility.FeatureSleepHooks: false, compatibility.FeatureMagSafeLED: false,
	} {
		if got := caps.Supports(feature); got != want {
			t.Errorf("%s supported=%t want=%t", feature, got, want)
		}
	}
}
