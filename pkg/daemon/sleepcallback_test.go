package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

// These tests drive the daemon through the same transitions IOKit delivers
// around sleep and wake, on a simulated Mac.

var _ SleepHandler = daemonSleepHandler{}

// newSleepHookFixture is a legacy Mac with an 80/75 band whose last wake was
// long ago, so the post-wake grace period never interferes.
func newSleepHookFixture(t *testing.T, opts ...fixtureOption) *fixture {
	t.Helper()
	f := newFixture(t, firmwareLegacy, opts...)
	f.mem.SetUpperLimit(80)
	f.mem.SetLowerLimit(75)
	f.d.lastWakeTime = time.Now().Add(-time.Hour)
	return f
}

// ---------------------------------------------------------------------------
// Idle sleep: kIOMessageCanSystemSleep
// ---------------------------------------------------------------------------

func TestCanSystemSleepAllowsWhenBattDoesNotControlCharging(t *testing.T) {
	// Firmware and native modes never run the hysteresis loop, so there is no
	// charging in progress to protect.
	for _, fw := range []firmware{firmwareManaged, firmwareGated} {
		f := newFixture(t, fw, withNative(sliderNative(80, 100)), withCharge(70), withCharging(true))
		f.mem.SetPreventIdleSleep(true)
		f.d.lastWakeTime = time.Now().Add(-time.Hour)
		if !f.d.canSystemSleep() {
			t.Errorf("%s: idle sleep must be allowed when batt does not control charging", fw.name)
		}
	}
}

func TestCanSystemSleepHonorsSettings(t *testing.T) {
	tests := []struct {
		name               string
		preventIdleSleep   bool
		preventSystemSleep bool
		charge             int
		charging           bool
		want               bool
	}{
		{name: "prevent-idle-sleep off", charge: 70, charging: true, want: true},
		{name: "prevent-system-sleep makes prevent-idle-sleep moot", preventIdleSleep: true, preventSystemSleep: true, charge: 70, charging: true, want: true},
		// 70% is below the 75% lower limit, so batt charges.
		{name: "charging below the limit vetoes idle sleep", preventIdleSleep: true, charge: 70, charging: true, want: false},
		{name: "below the limit batt starts charging and vetoes idle sleep", preventIdleSleep: true, charge: 70, charging: false, want: false},
		// 78% sits inside the band, where batt leaves charging off.
		{name: "not charging allows idle sleep", preventIdleSleep: true, charge: 78, charging: false, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSleepHookFixture(t, withCharge(tt.charge), withCharging(tt.charging))
			f.mem.SetPreventIdleSleep(tt.preventIdleSleep)
			f.mem.SetPreventSystemSleep(tt.preventSystemSleep)
			f.settleLoop()
			if got := f.d.canSystemSleep(); got != tt.want {
				t.Fatalf("canSystemSleep() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestCanSystemSleepRunsAMaintenancePassFirst(t *testing.T) {
	// The decision is based on fresh state: at 85% with charging left on, the
	// pass turns charging off and idle sleep is then allowed.
	f := newSleepHookFixture(t, withCharge(85), withCharging(true))
	f.mem.SetPreventIdleSleep(true)

	if !f.d.canSystemSleep() {
		t.Fatal("idle sleep must be allowed once the battery is above the limit")
	}
	if f.smc.chargingEnabled(t) {
		t.Fatal("the maintenance pass before the decision must have disabled charging")
	}
}

func TestCanSystemSleepDeniedRightAfterWake(t *testing.T) {
	// A maintain loop may still be waiting out the post-wake delay, so no
	// decision has been made yet.
	f := newSleepHookFixture(t, withCharge(85), withCharging(false))
	f.mem.SetPreventIdleSleep(true)
	f.d.preSleepLoopDelay = time.Hour
	f.d.lastWakeTime = time.Now()

	if f.d.canSystemSleep() {
		t.Fatal("idle sleep must be denied while the post-wake delay runs")
	}
	if f.smc.callCount("GetBatteryCharge") != 0 {
		t.Fatal("no maintenance pass may run during the post-wake delay")
	}
}

func TestCanSystemSleepIgnoresUnpluggedCharging(t *testing.T) {
	// On battery there is no maintained charging, whatever the switch says.
	f := newSleepHookFixture(t, withCharge(70), withCharging(true), withPluggedIn(false))
	f.mem.SetPreventIdleSleep(true)

	if !f.d.canSystemSleep() {
		t.Fatal("idle sleep must be allowed on battery")
	}
}

// ---------------------------------------------------------------------------
// Sleep: kIOMessageSystemWillSleep
// ---------------------------------------------------------------------------

func TestSystemWillSleepDisablesChargingAndDimsLED(t *testing.T) {
	f := newSleepHookFixture(t, withCharge(70), withCharging(true), withLED(smc.LEDOrange))
	f.mem.SetDisableChargingPreSleep(true)
	f.mem.SetControlMagSafeLED(config.ControlMagSafeModeEnabled)

	f.d.systemWillSleep()

	if f.smc.chargingEnabled(t) {
		t.Fatal("charging must be disabled before sleep")
	}
	if led := f.smc.led(t); led != smc.LEDOff {
		t.Fatalf("LED = %02x, want off", byte(led))
	}
}

func TestSystemWillSleepHoldsTheMaintainLoop(t *testing.T) {
	// macOS waits up to 30s before sleeping, and a maintain loop in that window
	// would re-enable charging. The loop is held back for the pre-sleep delay.
	f := newSleepHookFixture(t, withCharge(70), withCharging(true))
	f.mem.SetDisableChargingPreSleep(true)
	f.d.preSleepLoopDelay = 60 * time.Millisecond

	f.d.systemWillSleep()
	started := time.Now()
	f.d.maintainLoop()
	if waited := time.Since(started); waited < f.d.preSleepLoopDelay {
		t.Fatalf("maintain loop ran after %s, want at least %s", waited, f.d.preSleepLoopDelay)
	}
}

func TestSystemWillSleepLeavesChargingWhenDisabled(t *testing.T) {
	tests := []struct {
		name string
		set  func(*memConfig)
	}{
		{name: "disable-charging-pre-sleep off", set: func(c *memConfig) { c.SetDisableChargingPreSleep(false) }},
		{name: "prevent-system-sleep on", set: func(c *memConfig) {
			c.SetDisableChargingPreSleep(true)
			c.SetPreventSystemSleep(true)
		}},
		{name: "limit disabled", set: func(c *memConfig) {
			c.SetDisableChargingPreSleep(true)
			c.SetUpperLimit(100)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSleepHookFixture(t, withCharge(70), withCharging(true))
			tt.set(f.mem)

			f.d.systemWillSleep()

			if !f.smc.chargingEnabled(t) {
				t.Fatal("charging must be left alone")
			}
			started := time.Now()
			f.d.loopDelay.Wait()
			if time.Since(started) > 10*time.Millisecond {
				t.Fatal("the maintain loop must not be held back")
			}
		})
	}
}

func TestSystemWillSleepIgnoresManagedModes(t *testing.T) {
	f := newFixture(t, firmwareManaged, withCharge(70), withCharging(true))
	f.mem.SetDisableChargingPreSleep(true)
	f.mem.SetUpperLimit(80)

	f.d.systemWillSleep()

	if f.smc.callCount("DisableCharging") != 0 || f.smc.callCount("DisableAdapter") != 0 {
		t.Fatal("Apple-managed charge control must not be touched before sleep")
	}
}

func TestSystemWillSleepInAdapterModeCutsWallPower(t *testing.T) {
	f := newFixture(t, firmwareGated, withCapabilities(adapterCaps()), withCharge(70))
	f.mem.SetUpperLimit(80)
	f.mem.SetLowerLimit(75)
	f.mem.SetDisableChargingPreSleep(true)

	f.d.systemWillSleep()

	if f.smc.adapterEnabled(t) {
		t.Fatal("adapter mode must cut wall power before sleep so the battery cannot charge past the limit")
	}
}

func TestSystemWillSleepReportsASwitchFailure(t *testing.T) {
	f := newSleepHookFixture(t, withCharge(70), withCharging(true), withLED(smc.LEDOrange))
	f.mem.SetDisableChargingPreSleep(true)
	f.mem.SetControlMagSafeLED(config.ControlMagSafeModeEnabled)
	f.smc.fail("DisableCharging", errors.New("smc write failed"))

	f.d.systemWillSleep()

	if f.smc.callCount("DisableMagSafeLed") != 0 {
		t.Fatal("the LED must not be dimmed when charging could not be disabled")
	}
}

// ---------------------------------------------------------------------------
// Wake: kIOMessageSystemHasPoweredOn
// ---------------------------------------------------------------------------

func TestSystemHasPoweredOnDelaysTheLoopAndDimsLED(t *testing.T) {
	f := newSleepHookFixture(t, withCharge(70), withCharging(false), withLED(smc.LEDGreen))
	f.mem.SetDisableChargingPreSleep(true)
	f.mem.SetControlMagSafeLED(config.ControlMagSafeModeEnabled)
	f.d.postSleepLoopDelay = 60 * time.Millisecond
	f.settleLoop()
	before := time.Now()

	f.d.systemHasPoweredOn()

	if f.d.lastWakeTime.Before(before) {
		t.Fatal("the wake time must be recorded")
	}
	started := time.Now()
	f.d.maintainLoop()
	if waited := time.Since(started); waited < f.d.postSleepLoopDelay {
		t.Fatalf("maintain loop ran after %s, want at least %s", waited, f.d.postSleepLoopDelay)
	}
	if f.smc.callCount("DisableMagSafeLed") == 0 {
		t.Fatal("the LED must be dimmed while the loop waits after wake")
	}
	if !f.smc.chargingEnabled(t) {
		t.Fatal("once the delay has passed the loop must resume charging below the lower limit")
	}
}

func TestSystemHasPoweredOnRunsTheLoopAtOnceWithPreventSystemSleep(t *testing.T) {
	// With prevent-system-sleep the Mac wakes briefly on charger connection;
	// batt must decide right away whether to charge and keep it awake.
	f := newSleepHookFixture(t, withCharge(70), withCharging(false))
	f.mem.SetPreventSystemSleep(true)
	f.d.postSleepLoopDelay = time.Hour

	f.d.systemHasPoweredOn()

	if !f.smc.chargingEnabled(t) {
		t.Fatal("charging must start immediately after wake below the lower limit")
	}
	if !f.asserts.holdsSleepOnAC() {
		t.Fatal("the dark-wake assertion must be held while charging")
	}
	started := time.Now()
	f.d.loopDelay.Wait()
	if time.Since(started) > 10*time.Millisecond {
		t.Fatal("the maintain loop must not be held back")
	}
}

func TestSystemHasPoweredOnDoesNothingWithLimitDisabled(t *testing.T) {
	f := newSleepHookFixture(t, withCharge(70), withCharging(false))
	f.mem.SetUpperLimit(100)
	f.mem.SetDisableChargingPreSleep(true)

	f.d.systemHasPoweredOn()

	started := time.Now()
	f.d.loopDelay.Wait()
	if time.Since(started) > 10*time.Millisecond {
		t.Fatal("the maintain loop must not be held back")
	}
}

func TestSystemHasPoweredOnIgnoresManagedModes(t *testing.T) {
	f := newFixture(t, firmwareManaged, withCharge(70))
	f.mem.SetUpperLimit(80)
	f.mem.SetDisableChargingPreSleep(true)
	f.mem.SetControlMagSafeLED(config.ControlMagSafeModeEnabled)
	wake := f.d.lastWakeTime

	f.d.systemHasPoweredOn()

	if !f.d.lastWakeTime.Equal(wake) || f.smc.callCount("DisableMagSafeLed") != 0 {
		t.Fatal("managed modes have no post-wake work")
	}
}

func TestSleepWallClock(t *testing.T) {
	started := time.Now()
	sleepWallClock(0)
	if time.Since(started) > 10*time.Millisecond {
		t.Fatal("a zero wait must return at once")
	}
	started = time.Now()
	sleepWallClock(30 * time.Millisecond)
	if waited := time.Since(started); waited < 30*time.Millisecond {
		t.Fatalf("waited %s, want at least 30ms", waited)
	}
}

// ---------------------------------------------------------------------------
// Power assertions
// ---------------------------------------------------------------------------

func TestPreventSystemSleepHoldsAssertionWhileCharging(t *testing.T) {
	f := newSleepHookFixture(t, withCharge(70), withCharging(false))
	f.mem.SetPreventSystemSleep(true)

	if !f.d.maintainLoopForced() {
		t.Fatal("maintain loop failed")
	}
	if !f.smc.chargingEnabled(t) || !f.asserts.holdsSleepOnAC() {
		t.Fatal("charging below the lower limit must hold the dark-wake assertion")
	}

	f.smc.setCharge(t, 85)
	if !f.d.maintainLoopForced() {
		t.Fatal("maintain loop failed")
	}
	if f.smc.chargingEnabled(t) || f.asserts.holdsSleepOnAC() {
		t.Fatal("stopping at the limit must release the dark-wake assertion")
	}
}

func TestDisablingTheLimitReleasesAssertion(t *testing.T) {
	f := newSleepHookFixture(t, withCharge(70), withCharging(false))
	f.mem.SetPreventSystemSleep(true)
	if !f.d.maintainLoopForced() || !f.asserts.holdsSleepOnAC() {
		t.Fatal("precondition: charging must hold the assertion")
	}

	f.mem.SetUpperLimit(100)
	if !f.d.maintainLoopForced() {
		t.Fatal("maintain loop failed")
	}
	if f.asserts.holdsSleepOnAC() {
		t.Fatal("a disabled limit must release the dark-wake assertion")
	}
	if !f.smc.chargingEnabled(t) {
		t.Fatal("a disabled limit must let the battery charge")
	}
}

func TestWithoutPreventSystemSleepNoAssertionIsTaken(t *testing.T) {
	f := newSleepHookFixture(t, withCharge(70), withCharging(false))
	f.mem.SetPreventSystemSleep(false)

	if !f.d.maintainLoopForced() {
		t.Fatal("maintain loop failed")
	}
	if f.asserts.preventOnAC != 0 {
		t.Fatal("no assertion may be taken unless prevent-system-sleep is on")
	}
}

func TestShutdownReleasesAssertions(t *testing.T) {
	f := newSleepHookFixture(t, withCharge(70), withCharging(false))
	if err := f.asserts.PreventSleepOnAC(); err != nil {
		t.Fatal(err)
	}
	if err := f.asserts.PreventCalibrationSleep(); err != nil {
		t.Fatal(err)
	}

	if err := f.d.shutdown(); err != nil {
		t.Fatal(err)
	}
	if f.asserts.holdsSleepOnAC() || f.asserts.holdsCalibrationSleep() {
		t.Fatal("shutdown must release every power assertion")
	}
	_ = compatibility.FeatureSleepHooks
}
