package daemon

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/charlie0129/batt/pkg/compatibility"
)

func noDrainCaffeinate() []SleepAssertion {
	return []SleepAssertion{{PID: 4242, Type: "PreventUserIdleSystemSleep", Name: "caffeinate command-line tool"}}
}

func TestNoDrainIsOptInAndIdempotent(t *testing.T) {
	f := newExactNativeFixture(t)
	if f.mem != nil {
		t.Fatal("exact native fixture should use a file config")
	}
	if !f.d.maintainLoopForced() || f.native.noDrainEnabled || len(f.native.noDrainSets) != 0 {
		t.Fatal("no-drain was enabled without an explicit opt-in")
	}
	r := f.put("/no-drain", "true")
	if r.Code != http.StatusCreated || !f.file.NoDrain() || !f.native.noDrainEnabled || f.native.enabled {
		t.Fatalf("enable failed: %d %s config=%t native=%+v", r.Code, r.Body.String(), f.file.NoDrain(), f.native)
	}
	if got := f.d.getNoDrainStatus(); got.Phase != noDrainActive {
		t.Fatalf("status = %+v, want active", got)
	}
	calls := len(f.native.noDrainSets)
	if !f.d.maintainLoopForced() || len(f.native.noDrainSets) != calls {
		t.Fatal("steady-state maintenance rewrote the no-drain policy")
	}
}

func TestNoDrainDefersPowerdRestartForForeignAssertions(t *testing.T) {
	f := newExactNativeFixture(t)
	f.asserts.foreign = noDrainCaffeinate()
	r := f.put("/no-drain", "true")
	if r.Code != http.StatusCreated || !strings.Contains(r.Body.String(), "deferred") {
		t.Fatalf("deferred enable = %d %s", r.Code, r.Body.String())
	}
	if !f.file.NoDrain() || f.native.noDrainEnabled || len(f.native.noDrainSets) != 0 {
		t.Fatalf("deferred enable changed powerd: config=%t native=%+v", f.file.NoDrain(), f.native)
	}
	if !f.native.enabled || f.native.limit != 80 {
		t.Fatalf("ordinary native fallback missing: %+v", f.native)
	}
	if got := f.d.getNoDrainStatus(); got.Phase != noDrainWaiting {
		t.Fatalf("status = %+v, want waiting", got)
	}
	f.asserts.foreign = nil
	if !f.d.maintainLoopForced() || !f.native.noDrainEnabled || f.native.enabled {
		t.Fatalf("deferred policy did not apply after assertions ended: %+v", f.native)
	}
}

func TestNoDrainDefersTargetChangeWithoutDroppingOldCeiling(t *testing.T) {
	f := newExactNativeFixture(t)
	f.put("/no-drain", "true")
	f.asserts.foreign = noDrainCaffeinate()
	r := f.put("/limit", "70")
	if r.Code != http.StatusCreated || f.native.noDrainLimit != 80 || len(f.native.noDrainSets) != 1 {
		t.Fatalf("target change restarted powerd or dropped old policy: %d %s native=%+v", r.Code, r.Body.String(), f.native)
	}
	if f.native.enabled {
		t.Fatal("competing PowerUIAgent policy was enabled while waiting")
	}
	f.asserts.foreign = nil
	if !f.d.maintainLoopForced() || f.native.noDrainLimit != 70 || len(f.native.noDrainSets) != 2 {
		t.Fatalf("target did not apply after assertions ended: %+v", f.native)
	}
}

func TestNoDrainDisableAlsoWaitsForSafePowerdRestart(t *testing.T) {
	f := newExactNativeFixture(t)
	f.put("/no-drain", "true")
	f.asserts.foreign = noDrainCaffeinate()
	r := f.put("/no-drain", "false")
	if r.Code != http.StatusCreated || f.file.NoDrain() || !f.native.noDrainEnabled {
		t.Fatalf("disable did not preserve a safe pending transition: %d %s native=%+v", r.Code, r.Body.String(), f.native)
	}
	f.asserts.foreign = nil
	if !f.d.maintainLoopForced() || f.native.noDrainEnabled || !f.native.enabled || f.native.limit != 80 {
		t.Fatalf("regular native limit was not restored: %+v", f.native)
	}
}

func TestNoDrainDisableWithoutPolicyClearsDeferredStatus(t *testing.T) {
	f := newExactNativeFixture(t)
	f.asserts.foreign = noDrainCaffeinate()
	if r := f.put("/no-drain", "true"); r.Code != http.StatusCreated {
		t.Fatal(r.Body.String())
	}
	f.asserts.foreign = nil
	if r := f.put("/no-drain", "false"); r.Code != http.StatusCreated {
		t.Fatal(r.Body.String())
	}
	if got := f.d.getNoDrainStatus(); got.Phase != noDrainOff {
		t.Fatalf("status = %+v, want off", got)
	}
}

func TestNoDrainPolicyFailureRestoresRegularNativeLimit(t *testing.T) {
	f := newExactNativeFixture(t)
	f.native.noDrainSetErr = errors.New("unrecognized policy archive")
	r := f.put("/no-drain", "true")
	if r.Code != http.StatusCreated || !strings.Contains(r.Body.String(), "turned off") {
		t.Fatalf("fallback response = %d %s", r.Code, r.Body.String())
	}
	if f.file.NoDrain() || !f.native.enabled || f.native.limit != 80 || f.native.noDrainEnabled {
		t.Fatalf("fallback did not restore regular native control: config=%t native=%+v", f.file.NoDrain(), f.native)
	}
	if got := f.d.getNoDrainStatus(); got.Phase != noDrainError {
		t.Fatalf("status = %+v, want error", got)
	}
}

func TestNoDrainDisableKeepsRequestedStateWhenNativeRestoreFails(t *testing.T) {
	f := newExactNativeFixture(t)
	f.put("/no-drain", "true")
	f.native.err = errors.New("PowerUIAgent is reloading")
	r := f.put("/no-drain", "false")
	if r.Code != http.StatusCreated || f.file.NoDrain() || f.native.noDrainEnabled {
		t.Fatalf("disable rolled back or left policy active: %d %s config=%t native=%+v", r.Code, r.Body.String(), f.file.NoDrain(), f.native)
	}
}

func TestNoDrainChargeFullReleasesCeiling(t *testing.T) {
	f := newExactNativeFixture(t, withCharge(50))
	f.put("/no-drain", "true")
	r := f.post("/charge-once/full")
	if r.Code != http.StatusCreated || f.native.noDrainEnabled || f.file.ChargeOnceTarget() != 100 {
		t.Fatalf("charge full did not release no-drain: %d %s native=%+v", r.Code, r.Body.String(), f.native)
	}
}

func TestNoDrainRequiresExactNativeMode(t *testing.T) {
	f := newFixture(t, firmwareGated, withFileConfig(), withNative(sliderNative(80, 100)))
	r := f.put("/no-drain", "true")
	if r.Code != http.StatusConflict || f.file.NoDrain() {
		t.Fatalf("slider-only native accepted no-drain: %d %s", r.Code, r.Body.String())
	}
	f = newFixture(t, firmwareLegacy, withFileConfig())
	r = f.put("/no-drain", "true")
	if r.Code != http.StatusConflict || f.file.NoDrain() {
		t.Fatalf("legacy accepted no-drain: %d %s", r.Code, r.Body.String())
	}
	f = newFixture(t, firmwareGated, withFileConfig(), withNative(exactNative()))
	f.file.SetAdapterMode(true)
	f.setCaps(adapterCaps())
	r = f.put("/no-drain", "true")
	if r.Code != http.StatusConflict || f.file.NoDrain() {
		t.Fatalf("adapter mode accepted no-drain: %d %s", r.Code, r.Body.String())
	}
}

func TestNoDrainAssertionReadFailureDefersRatherThanRestarting(t *testing.T) {
	f := newExactNativeFixture(t)
	f.asserts.foreignErr = errors.New("assertion service unavailable")
	r := f.put("/no-drain", "true")
	if r.Code != http.StatusCreated || len(f.native.noDrainSets) != 0 || !f.native.enabled {
		t.Fatalf("assertion read failure was not safely deferred: %d %s native=%+v", r.Code, r.Body.String(), f.native)
	}
}

func TestNoDrainCapabilitiesRemainNativeOnly(t *testing.T) {
	f := newExactNativeFixture(t)
	if got := f.caps(); got.ChargeControlMode != compatibility.ChargeControlNative || !got.NativeExactTargets {
		t.Fatalf("unexpected capabilities: %+v", got)
	}
}
