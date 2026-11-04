package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
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

func TestExactNativeChargeNowIsRejected(t *testing.T) {
	file, _ := useTempConfig(t)
	file.SetUpperLimit(55)
	file.SetLowerLimit(50)
	fake := useExactNativeTargets(t)
	stubAdapter(t, true)
	stubBatteryCharge(t, 52)

	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/charge-once/limit", nil))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), ErrNativeChargeNow.Error()) {
		t.Fatalf("native charge now must be rejected: %d %s", response.Code, response.Body.String())
	}
	if file.ChargeOnceTarget() != 0 || file.UpperLimit() != 55 || file.LowerLimit() != 50 || len(fake.setCalls) != 0 || fake.disables != 0 {
		t.Fatalf("rejected charge now changed the configuration or native limit: target=%d native=%+v", file.ChargeOnceTarget(), fake)
	}
	if maintainManagedChargeOnce(55) || len(fake.setCalls) != 0 || fake.disables != 0 {
		t.Fatalf("native maintenance must reject charge now without changing the limit: %+v", fake)
	}
}

func TestExactNativeChargeOnceRestartRecovery(t *testing.T) {
	for _, tc := range []struct {
		name         string
		target, want int
	}{
		{name: "charge now is cleared", target: 55, want: 0},
		{name: "charge full is preserved", target: 100, want: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, path := useTempConfig(t)
			file.SetUpperLimit(55)
			file.SetChargeOnceTarget(tc.target)
			if err := file.Save(); err != nil {
				t.Fatal(err)
			}
			fake := useExactNativeTargets(t)
			stubAdapter(t, true)
			mockNativeBatteryCharge(t, 52)

			disableUnsupportedConfiguredFeatures()
			reloaded, err := config.NewFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if file.ChargeOnceTarget() != tc.want || reloaded.ChargeOnceTarget() != tc.want {
				t.Fatalf("restart target=%d: in memory=%d, on disk=%d, want=%d", tc.target, file.ChargeOnceTarget(), reloaded.ChargeOnceTarget(), tc.want)
			}
			if !maintainLoopForced() || fake.enabled != (tc.want == 0) || (tc.want == 0 && fake.limit != 55) {
				t.Fatalf("restart did not apply the supported target: target=%d native=%+v", tc.want, fake)
			}
		})
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
