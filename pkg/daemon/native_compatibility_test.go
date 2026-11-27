package daemon

import (
	"net/http"
	"strings"
	"testing"

	"github.com/charlie0129/batt/pkg/compatibility"
)

func TestExactNativeChargeNowIsRejected(t *testing.T) {
	f := newExactNativeFixture(t, withCharge(52))
	f.file.SetUpperLimit(55)
	f.file.SetLowerLimit(50)

	response := f.post("/charge-once/limit")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), ErrNativeChargeNow.Error()) {
		t.Fatalf("native charge now must be rejected: %d %s", response.Code, response.Body.String())
	}
	if f.file.ChargeOnceTarget() != 0 || f.file.UpperLimit() != 55 || f.file.LowerLimit() != 50 || len(f.native.setCalls) != 0 || f.native.disables != 0 {
		t.Fatalf("rejected charge now changed the configuration or native limit: target=%d native=%+v", f.file.ChargeOnceTarget(), f.native)
	}
	if f.d.maintainManagedChargeOnce(55) || len(f.native.setCalls) != 0 || f.native.disables != 0 {
		t.Fatalf("native maintenance must reject charge now without changing the limit: %+v", f.native)
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
			f := newExactNativeFixture(t, withCharge(52))
			f.file.SetUpperLimit(55)
			f.file.SetChargeOnceTarget(tc.target)
			if err := f.file.Save(); err != nil {
				t.Fatal(err)
			}

			f.d.disableUnsupportedConfiguredFeatures()
			reloaded := f.reloadFile()
			if f.file.ChargeOnceTarget() != tc.want || reloaded.ChargeOnceTarget() != tc.want {
				t.Fatalf("restart target=%d: in memory=%d, on disk=%d, want=%d", tc.target, f.file.ChargeOnceTarget(), reloaded.ChargeOnceTarget(), tc.want)
			}
			if !f.d.maintainLoopForced() || f.native.enabled != (tc.want == 0) || (tc.want == 0 && f.native.limit != 55) {
				t.Fatalf("restart did not apply the supported target: target=%d native=%+v", tc.want, f.native)
			}
		})
	}
}

func TestNativeFullChargeRestoresTargetAt100Percent(t *testing.T) {
	f := newExactNativeFixture(t, withCharge(52))
	f.file.SetUpperLimit(55)
	response := f.post("/charge-once/full")
	if response.Code != http.StatusCreated || f.file.ChargeOnceTarget() != 100 || f.native.enabled {
		t.Fatalf("full charge failed: %d %s", response.Code, response.Body.String())
	}
	f.smc.setCharge(t, 100)
	if !f.d.completeChargeOnce() || !f.d.maintainLoopForced() || !f.native.enabled || f.native.limit != 55 {
		t.Fatal("reaching 100% did not restore the configured target")
	}
}

func TestEarlierNativeBackendKeepsPreviousPaths(t *testing.T) {
	f := newFixture(t, firmwareGatedNoAdapter, withFileConfig(), withNative(&fakeNativeLimit{limit: 100}),
		withCapabilities(nativeCaps(80, 85, 90, 95, 100)))
	f.file.SetUpperLimit(80)
	if !f.d.maintainLoopForced() || f.native.limit != 80 {
		t.Fatal("earlier native backend did not use its slider target")
	}
	if response := f.put("/lower-limit-delta", "5"); response.Code != http.StatusConflict {
		t.Fatal("earlier native backend gained an unsupported lower-limit command")
	}
	f.file.SetUpperLimit(100)
	f.native.limit, f.native.enabled, f.native.registered = 100, false, true
	if _, err := f.d.ensureNativeChargeLimitDisabled(); err != nil || f.native.disables != 0 {
		t.Fatal("earlier backend entered the macOS 27 registration cleanup path")
	}
}

func TestNativeFeatureCompatibility(t *testing.T) {
	caps := compatibility.Capabilities{
		ChargingControl: true, ChargeControlMode: compatibility.ChargeControlNative,
		NativeExactTargets: true, AdapterControl: true, Calibration: true,
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
