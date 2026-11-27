package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/powerinfo"
)

func TestNativeLimitUsesConfiguredTargetRegardlessOfCharge(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		charge, target, previous int
	}{
		{"full battery", 100, 97, 100},
		{"above limit paused", 70, 55, 70},
		{"above limit charging", 70, 55, 80},
		{"near full", 99, 50, 99},
		{"above requested target", 60, 50, 60},
		{"at requested target", 50, 50, 50},
		{"inside old band", 52, 55, 52},
		{"below minimum", 8, 55, 8},
		{"minimum native target", 8, 10, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newExactNativeFixture(t, withCharge(tc.charge))
			f.native.limit, f.native.enabled = tc.previous, tc.previous < 100
			r := f.put("/limit", strconv.Itoa(tc.target))
			if r.Code != http.StatusCreated || f.native.limit != tc.target || !f.native.enabled {
				t.Fatalf("limit not applied: %d %s native=%+v", r.Code, r.Body.String(), f.native)
			}
			wantMessage := fmt.Sprintf("set charging limit to %d%%, current charge: %d%%. macOS resumes charging once the battery drops about 5%% below the limit.", tc.target, tc.charge)
			if tc.charge > tc.target {
				wantMessage += " Current charge is above the limit; macOS may use battery power to discharge toward the target."
			}
			if strings.TrimSpace(r.Body.String()) != strconv.Quote(wantMessage) || f.file.UpperLimit() != tc.target {
				t.Fatalf("unexpected native limit response or saved target: %s target=%d", r.Body.String(), f.file.UpperLimit())
			}
			f.d.conf = f.reloadFile() // daemon restart
			if !f.d.maintainLoopForced() || f.native.limit != tc.target || !f.native.enabled || f.native.disables != 0 {
				t.Fatal("restart changed or released the configured target")
			}
		})
	}
}

func TestNativePendingLimitIsAccepted(t *testing.T) {
	// While the battery charges, macOS applies a new target only once the
	// battery level changes or charging stops. That is not a failure (#174).
	f := newExactNativeFixture(t, withCharge(75))
	f.native.limit, f.native.enabled = 80, true
	f.native.deferRegistration = true

	r := f.put("/limit", "70")
	if r.Code != http.StatusCreated || !strings.Contains(r.Body.String(), powerinfo.ChargeLimitPendingNotice) || f.file.UpperLimit() != 70 {
		t.Fatalf("pending limit not accepted: %d %s", r.Code, r.Body.String())
	}
	if !f.chargeLimitPending(t) {
		t.Fatal("battery info hides the pending limit")
	}
	if !f.d.maintainLoopForced() || !f.chargeLimitPending(t) {
		t.Fatal("next pass treated the pending limit as a failure or forgot it")
	}

	f.native.register()
	if !f.d.maintainLoopForced() || f.native.limit != 70 || f.chargeLimitPending(t) {
		t.Fatalf("applied limit still reported pending: %+v", f.native)
	}
	r = f.put("/limit", "65")
	if r.Code != http.StatusCreated || strings.Contains(r.Body.String(), powerinfo.ChargeLimitPendingNotice) {
		t.Fatalf("applied limit reported pending: %d %s", r.Code, r.Body.String())
	}

	// Releasing the limit while a target is pending goes through at once.
	f.native.deferRegistration = true
	if r := f.put("/limit", "60"); r.Code != http.StatusCreated || !f.chargeLimitPending(t) {
		t.Fatalf("second pending limit not accepted: %d %s", r.Code, r.Body.String())
	}
	if r := f.put("/limit", "100"); r.Code != http.StatusCreated || f.native.enabled || f.native.pending != 0 || f.chargeLimitPending(t) {
		t.Fatalf("pending limit not released: %d %s native=%+v", r.Code, r.Body.String(), f.native)
	}
}

func TestNativeCancelledFullChargeReportsPendingLimit(t *testing.T) {
	// Cancelling a charge to full restores the limit while the battery is
	// still charging, which is exactly when macOS defers a new target.
	f := newExactNativeFixture(t, withCharge(75))
	f.file.SetUpperLimit(70)
	f.file.SetLowerLimit(68)
	if r := f.post(chargeOnceFullPath); r.Code != http.StatusCreated || f.native.enabled {
		t.Fatalf("charge to full not started: %d %s native=%+v", r.Code, r.Body.String(), f.native)
	}
	f.native.deferRegistration = true
	r := f.post(chargeOnceCancelPath)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "applies again. "+powerinfo.ChargeLimitPendingNotice) || f.native.pending != 70 {
		t.Fatalf("cancel hid the pending limit: %d %s native=%+v", r.Code, r.Body.String(), f.native)
	}
}

func TestStalePendingNativeLimitAfterAdapterMode(t *testing.T) {
	// Adapter mode never refreshes the pending native target, so one left
	// over from native mode must not explain an adapter-mode limit.
	f := newExactNativeFixture(t, withCharge(75))
	f.native.limit, f.native.enabled = 80, true
	f.native.deferRegistration = true
	if r := f.put("/limit", "70"); r.Code != http.StatusCreated || !strings.Contains(r.Body.String(), powerinfo.ChargeLimitPendingNotice) {
		t.Fatalf("native limit not pending: %d %s", r.Code, r.Body.String())
	}
	if r := f.put("/adapter-mode", "true"); r.Code != http.StatusCreated || f.caps().ChargeControlMode != compatibility.ChargeControlAdapter {
		t.Fatalf("adapter mode not entered: %d %s", r.Code, r.Body.String())
	}
	r := f.put("/limit", "70")
	if r.Code != http.StatusCreated || strings.Contains(r.Body.String(), powerinfo.ChargeLimitPendingNotice) {
		t.Fatalf("adapter-mode limit explained as a pending native limit: %d %s", r.Code, r.Body.String())
	}
}

func TestNativePendingLimitOnBattery(t *testing.T) {
	// After 5 minutes on battery power macOS withdraws the native limit, and
	// it registers it again once the Mac is connected to power (#174).
	f := newExactNativeFixture(t, withCharge(87), withPluggedIn(false), withAdapter(false))
	f.native.limit, f.native.enabled = 80, true
	f.native.deferRegistration = true

	r := f.put("/limit", "70")
	if r.Code != http.StatusCreated || !strings.Contains(r.Body.String(), nativeLimitOnBatteryNotice) ||
		strings.Contains(r.Body.String(), powerinfo.ChargeLimitPendingNotice) {
		t.Fatalf("limit on battery power not accepted with the battery notice: %d %s", r.Code, r.Body.String())
	}
	if !f.d.maintainLoopForced() || f.chargeLimitPending(t) {
		t.Fatal("limit on battery power failed, or was reported as waiting for macOS")
	}

	// Connected to power, but macOS has yet to apply the limit.
	f.smc.setPluggedIn(t, true)
	f.smc.setAdapter(t, true)
	if !f.chargeLimitPending(t) {
		t.Fatal("pending limit hidden on wall power")
	}
	f.native.register()
	if !f.d.maintainLoopForced() || f.native.limit != 70 || f.chargeLimitPending(t) {
		t.Fatalf("limit not applied after connecting to power: %+v", f.native)
	}
}

// chargeLimitPending reports whether /battery-info says macOS has yet to apply
// the native limit.
func (f *fixture) chargeLimitPending(t *testing.T) bool {
	t.Helper()
	var battery powerinfo.Battery
	if err := json.Unmarshal(f.mustServe(http.MethodGet, "/battery-info", "", http.StatusOK).Body.Bytes(), &battery); err != nil {
		t.Fatal(err)
	}
	if battery.ChargeLimitPending == nil {
		t.Fatal("exact native mode must report whether its limit is pending")
	}
	return *battery.ChargeLimitPending
}

func TestNativeTargetDoesNotFollowUnpluggedBattery(t *testing.T) {
	f := newExactNativeFixture(t, withPluggedIn(false), withAdapter(false))
	f.file.SetUpperLimit(55)
	f.file.SetLowerLimit(50)
	for _, charge := range []int{52, 51, 49, 8} {
		f.smc.setCharge(t, charge)
		if !f.d.maintainLoopForced() || f.native.limit != 55 {
			t.Fatalf("unplugged battery at %d%% changed the target to %d%%", charge, f.native.limit)
		}
	}
	f.smc.setPluggedIn(t, true)
	f.smc.setAdapter(t, true)
	if !f.d.maintainLoopForced() || f.native.limit != 55 || len(f.native.setCalls) != 1 {
		t.Fatal("reconnect changed or rewrote the target")
	}
}

func TestNativeLowerLimitDeltaIsAccepted(t *testing.T) {
	f := newExactNativeFixture(t, withCharge(30))
	f.file.SetUpperLimit(55)
	f.file.SetLowerLimit(50)
	r := f.put("/lower-limit-delta", "10")
	if r.Code != http.StatusCreated || f.file.LowerLimit() != 45 {
		t.Fatalf("native lower limit rejected: %d %s", r.Code, r.Body.String())
	}
	// Below the lower limit the battery charges to the upper limit.
	if !f.native.enabled || f.native.limit != 55 {
		t.Fatalf("native target = %+v, want 55", f.native)
	}
}

func TestNativeBandHoldsTheChargeBetweenTheLimits(t *testing.T) {
	f := newExactNativeFixture(t, withCharge(80))
	f.file.SetUpperLimit(80)
	f.file.SetLowerLimit(55)
	f.d.maintainLoopForced()

	// Between the limits the target is the current charge, so macOS stops
	// charging and runs from wall power.
	f.smc.setCharge(t, 70)
	if !f.d.maintainLoopForced() || !f.native.enabled || f.native.limit != 70 {
		t.Fatalf("native target = %+v, want a hold at 70", f.native)
	}

	// The hold point follows the charge down, never up.
	f.smc.setCharge(t, 69)
	if !f.d.maintainLoopForced() || f.native.limit != 69 {
		t.Fatalf("native target = %d, want 69", f.native.limit)
	}
	f.smc.setCharge(t, 55)
	if !f.d.maintainLoopForced() || f.native.limit != 55 {
		t.Fatalf("native target = %d at the lower limit, want 55", f.native.limit)
	}

	// Below the lower limit macOS charges to the upper limit, and keeps doing
	// so while the battery passes back through the band.
	for _, charge := range []int{54, 60, 79} {
		f.smc.setCharge(t, charge)
		if !f.d.maintainLoopForced() || f.native.limit != 80 {
			t.Fatalf("native target = %d at %d%% while charging, want 80", f.native.limit, charge)
		}
	}

	// At the upper limit the band holds again.
	f.smc.setCharge(t, 80)
	f.d.maintainLoopForced()
	f.smc.setCharge(t, 79)
	if !f.d.maintainLoopForced() || f.native.limit != 79 {
		t.Fatalf("native target = %d after reaching the limit, want a hold at 79", f.native.limit)
	}
}

func TestNativeBandResumesAChargeInProgress(t *testing.T) {
	// The daemon restarts while macOS charges toward the upper limit.
	f := newExactNativeFixture(t, withCharge(60))
	f.file.SetUpperLimit(80)
	f.file.SetLowerLimit(55)
	f.native.limit, f.native.enabled = 80, true
	if !f.d.maintainLoopForced() || f.native.limit != 80 || len(f.native.setCalls) != 0 {
		t.Fatalf("restart interrupted the charge: %+v", f.native)
	}
}

func TestNativeBandResumesChargingAfterTheLimitWasReleased(t *testing.T) {
	// The daemon releases the native limit on exit, so after a restart macOS
	// has been charging freely. Finish the charge to the upper limit.
	f := newExactNativeFixture(t, withCharge(70))
	f.file.SetUpperLimit(80)
	f.file.SetLowerLimit(55)
	f.native.limit, f.native.enabled = 100, false
	if !f.d.maintainLoopForced() || f.native.limit != 80 {
		t.Fatalf("native target = %d after a restart, want 80", f.native.limit)
	}
}

func TestNativeBandHoldsBelowAStaleHoldPointAfterARestart(t *testing.T) {
	// The daemon was killed while holding at 70; the battery has not moved.
	f := newExactNativeFixture(t, withCharge(70))
	f.file.SetUpperLimit(80)
	f.file.SetLowerLimit(55)
	f.native.limit, f.native.enabled = 70, true
	if !f.d.maintainLoopForced() || f.native.limit != 70 || len(f.native.setCalls) != 0 {
		t.Fatalf("native target = %+v, want the hold at 70 kept", f.native)
	}
}

func TestNativeBandHoldPointNeverExceedsTheUpperLimit(t *testing.T) {
	// After Apple's periodic full charge the battery is above the limit.
	f := newExactNativeFixture(t, withCharge(100))
	f.file.SetUpperLimit(80)
	f.file.SetLowerLimit(55)
	if !f.d.maintainLoopForced() || f.native.limit != 80 {
		t.Fatalf("native target = %d above the limit, want 80", f.native.limit)
	}
}

func TestNativeBandIsOffForNarrowDeltas(t *testing.T) {
	// macOS already resumes charging about 5% below its target. A narrower
	// band would top up more often, so the single native target is kept.
	for _, lower := range []int{78, 75} {
		f := newExactNativeFixture(t, withCharge(77))
		f.file.SetUpperLimit(80)
		f.file.SetLowerLimit(lower)
		if !f.d.maintainLoopForced() || f.native.limit != 80 {
			t.Fatalf("lower %d: native target = %d, want 80", lower, f.native.limit)
		}
	}
}

func TestNativeBandIsOffWithNoDrain(t *testing.T) {
	// No-drain keeps one powerd ceiling; moving it would restart powerd on
	// every percent.
	f := newExactNativeFixture(t, withCharge(70))
	f.file.SetUpperLimit(80)
	f.file.SetLowerLimit(55)
	f.file.SetNoDrain(true)
	if f.d.usesNativeBand(f.file.LowerLimit(), f.file.UpperLimit()) {
		t.Fatal("the band must stay off while no-drain is enabled")
	}
}

func TestNativeBandCatchesUpAfterReconnecting(t *testing.T) {
	f := newExactNativeFixture(t, withCharge(80))
	f.file.SetUpperLimit(80)
	f.file.SetLowerLimit(55)
	f.d.maintainLoopForced()
	f.smc.setCharge(t, 70)
	if !f.d.maintainLoopForced() || f.native.limit != 70 {
		t.Fatalf("native target = %d, want a hold at 70", f.native.limit)
	}

	// On battery power the target is left alone.
	f.smc.setPluggedIn(t, false)
	f.smc.setAdapter(t, false)
	writes := len(f.native.setCalls)
	for _, charge := range []int{68, 62} {
		f.smc.setCharge(t, charge)
		if !f.d.maintainLoopForced() || len(f.native.setCalls) != writes {
			t.Fatalf("unplugged battery at %d%% rewrote the target: %v", charge, f.native.setCalls)
		}
	}

	// Reconnected inside the band: hold where the battery is, rather than
	// topping it back up.
	f.smc.setPluggedIn(t, true)
	f.smc.setAdapter(t, true)
	if !f.d.maintainLoopForced() || f.native.limit != 62 {
		t.Fatalf("native target = %d after reconnecting at 62%%, want 62", f.native.limit)
	}
}

func TestNativeMinimumTargetIgnoresSavedLowerLimitDelta(t *testing.T) {
	f := newExactNativeFixture(t)
	f.file.SetUpperLimit(80)
	f.file.SetLowerLimit(30)
	r := f.put("/limit", "10")
	if r.Code != http.StatusCreated || f.native.limit != 10 || f.file.UpperLimit() != 10 {
		t.Fatalf("saved delta blocked minimum native target: %d %s", r.Code, r.Body.String())
	}
}

func TestNativeMinimumTargetSaveFailureRestoresLimits(t *testing.T) {
	f := newExactNativeFixture(t)
	f.file.SetUpperLimit(80)
	f.file.SetLowerLimit(30)
	if err := os.Mkdir(f.path, 0755); err != nil {
		t.Fatal(err)
	}
	r := f.put("/limit", "10")
	if r.Code != http.StatusInternalServerError || f.file.UpperLimit() != 80 || f.file.LowerLimit() != 30 || len(f.native.setCalls) != 0 {
		t.Fatalf("failed save changed limits: %d %s config=%d/%d", r.Code, r.Body.String(), f.file.UpperLimit(), f.file.LowerLimit())
	}
}
