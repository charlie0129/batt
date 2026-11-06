package daemon

import (
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/compatibility"
)

// These tests run the daemon's main loop against a battery model for a long
// stretch of simulated time, one firmware at a time. The model is crude: the
// charge moves one percent per loop, up while the battery may charge from
// wall power and down otherwise.

// simulateBattery runs loops iterations of the main loop. charging reports
// whether the battery is charging given the simulated hardware; it is what
// the firmware or batt decided, not what the config asks for.
func simulateBattery(t *testing.T, f *fixture, loops int, charging func() bool) (minCharge, maxCharge int) {
	t.Helper()
	charge, err := f.smc.AppleSMC.GetBatteryCharge()
	if err != nil {
		t.Fatal(err)
	}
	minCharge, maxCharge = 100, 0
	now := time.Now()
	for i := 0; i < loops; i++ {
		f.d.loopOnce(now)
		now = now.Add(f.d.loopInterval)
		if charging() {
			charge++
		} else {
			charge--
		}
		if charge > 100 {
			charge = 100
		}
		if charge < 0 {
			charge = 0
		}
		f.smc.setCharge(t, charge)
		if i >= loops/2 {
			// Only judge the steady state.
			minCharge = min(minCharge, charge)
			maxCharge = max(maxCharge, charge)
		}
	}
	return minCharge, maxCharge
}

func TestLegacyMacHoldsTheChargeWithinTheBand(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withCharge(30), withCharging(true))
	f.mem.SetUpperLimit(80)
	f.mem.SetLowerLimit(75)

	lo, hi := simulateBattery(t, f, 400, func() bool { return f.smc.chargingEnabled(t) })

	// The limit is reached from below and the charge then oscillates inside
	// the band: batt stops charging at 80 and resumes below 75.
	if hi > 80 || lo < 74 {
		t.Fatalf("steady-state charge ranged %d..%d, want within the 75/80 band", lo, hi)
	}
}

func TestLegacyMacWithoutALimitChargesToFull(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withCharge(30), withCharging(false))
	f.mem.SetUpperLimit(100)

	_, hi := simulateBattery(t, f, 200, func() bool { return f.smc.chargingEnabled(t) })
	if hi != 100 {
		t.Fatalf("steady-state maximum = %d, want 100", hi)
	}
}

func TestAdapterModeMacHoldsTheChargeWithinTheBand(t *testing.T) {
	f := newFixture(t, firmwareGated, withCharge(30), withCapabilities(adapterCaps()))
	f.mem.SetUpperLimit(60)
	f.mem.SetLowerLimit(55)

	// In adapter mode the battery charges while wall power is on and
	// discharges while batt has cut it.
	lo, hi := simulateBattery(t, f, 400, func() bool { return f.smc.adapterEnabled(t) })
	if hi > 60 || lo < 54 {
		t.Fatalf("steady-state charge ranged %d..%d, want within the 55/60 band", lo, hi)
	}
	if !f.smc.adapterEnabled(t) && f.caps().ChargeControlMode != compatibility.ChargeControlAdapter {
		t.Fatal("adapter mode must stay selected")
	}
}

func TestFirmwareManagedMacKeepsTheLimitInSync(t *testing.T) {
	// The firmware enforces the band itself; batt only keeps the keys in sync
	// with the configuration, including after the user changes it.
	f := newFixture(t, firmwareManaged, withCharge(30))
	f.mem.SetUpperLimit(80)
	f.mem.SetLowerLimit(78)

	// Model the firmware: it charges below the lower limit and stops at the
	// upper limit, as long as the limit is active.
	charging := true
	simulateBattery(t, f, 100, func() bool {
		limit := f.smc.firmwareLimit(t)
		charge, _ := f.smc.AppleSMC.GetBatteryCharge()
		switch {
		case !limit.Active:
			charging = charge < 100
		case charge >= int(limit.Upper):
			charging = false
		case charge < int(limit.Lower):
			charging = true
		}
		return charging
	})
	if limit := f.smc.firmwareLimit(t); !limit.Active || limit.Lower != 78 || limit.Upper != 80 {
		t.Fatalf("firmware limit = %+v, want active 78/80", limit)
	}
	if f.smc.callCount("EnsureFirmwareChargeLimit") == 0 {
		t.Fatal("the loop must reconcile the firmware limit")
	}

	f.mem.SetUpperLimit(60)
	f.mem.SetLowerLimit(55)
	f.d.loopOnce(time.Now())
	if limit := f.smc.firmwareLimit(t); !limit.Active || limit.Lower != 55 || limit.Upper != 60 {
		t.Fatalf("firmware limit = %+v, want active 55/60 after the change", limit)
	}

	f.mem.SetUpperLimit(100)
	f.d.loopOnce(time.Now())
	if limit := f.smc.firmwareLimit(t); limit.Active {
		t.Fatalf("firmware limit = %+v, want inactive at 100%%", limit)
	}
}

func TestNativeMacKeepsTheLimitInSync(t *testing.T) {
	f := newExactNativeFixture(t, withCharge(30))
	f.file.SetUpperLimit(70)

	simulateBattery(t, f, 50, func() bool {
		charge, _ := f.smc.AppleSMC.GetBatteryCharge()
		return !f.native.enabled || charge < f.native.limit
	})
	if !f.native.enabled || f.native.limit != 70 {
		t.Fatalf("native limit = %+v, want 70", f.native)
	}
	if len(f.native.setCalls) != 1 {
		t.Fatalf("the native limit was written %d times, want once", len(f.native.setCalls))
	}

	// macOS drifted (the user moved the slider); batt takes the limit back.
	f.native.limit = 90
	f.d.loopOnce(time.Now())
	if f.native.limit != 70 {
		t.Fatalf("native limit = %d after drift, want 70", f.native.limit)
	}
}

func TestLegacyMacTemporaryDisableRoundTrip(t *testing.T) {
	// "batt disable --for": the limit goes to 100, the battery charges past
	// the old limit, and the limit comes back when the timer elapses.
	f := newFixture(t, firmwareLegacy, withCharge(78), withCharging(false))
	f.mem.SetUpperLimit(80)
	f.mem.SetLowerLimit(75)

	f.mustServe("PUT", "/disable", `"1h"`, 201)
	if f.mem.UpperLimit() != 100 || f.mem.PreDisableLimit() != 80 {
		t.Fatalf("temporary disable not recorded: %+v", f.mem.snapshot())
	}
	_, hi := simulateBattery(t, f, 60, func() bool { return f.smc.chargingEnabled(t) })
	if hi < 95 {
		t.Fatalf("the battery must charge past the old limit while disabled, reached %d", hi)
	}

	// The deadline passes.
	f.mem.SetDisableTimer(time.Now().Add(-time.Second), 80)
	lo, hi := simulateBattery(t, f, 100, func() bool { return f.smc.chargingEnabled(t) })
	if f.mem.UpperLimit() != 80 || !f.mem.DisableUntil().IsZero() {
		t.Fatalf("limit not restored: %+v", f.mem.snapshot())
	}
	if hi > 80 || lo < 74 {
		t.Fatalf("steady-state charge ranged %d..%d after the restore, want within the band", lo, hi)
	}
}
