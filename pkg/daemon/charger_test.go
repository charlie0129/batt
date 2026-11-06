package daemon

import (
	"errors"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/compatibility"
)

// fakeCharger records the hysteresis loop's decisions.
type fakeCharger struct {
	enabled  bool
	enables  int
	disables int
	err      error
}

func (f *fakeCharger) IsEnabled() (bool, error) { return f.enabled, f.err }
func (f *fakeCharger) Enable() error {
	if f.err != nil {
		return f.err
	}
	f.enables++
	f.enabled = true
	return nil
}
func (f *fakeCharger) Disable() error {
	if f.err != nil {
		return f.err
	}
	f.disables++
	f.enabled = false
	return nil
}

// useCharger replaces the daemon's charge switch, keeping its capabilities.
func (f *fixture) useCharger(c chargeSwitch) {
	f.d.loopMu.Lock()
	defer f.d.loopMu.Unlock()
	f.d.storeChargeControl(f.d.getCapabilities(), c)
}

// newAdapterLoopFixture is a macOS 27 Mac in adapter mode with a 60/55 band.
func newAdapterLoopFixture(t *testing.T, charge int, pluggedIn, adapterOn bool) *fixture {
	t.Helper()
	f := newFixture(t, firmwareGated,
		withCapabilities(adapterCaps()),
		withCharge(charge), withPluggedIn(pluggedIn), withAdapter(adapterOn))
	f.conf.SetUpperLimit(60)
	f.conf.SetLowerLimit(55)
	return f
}

func TestSelectCharger(t *testing.T) {
	f := newFixture(t, firmwareLegacy)
	if _, ok := f.d.selectCharger(compatibility.ChargeControlAdapter).(adapterSwitch); !ok {
		t.Fatal("adapter mode must use the adapter switch")
	}
	for _, mode := range []compatibility.ChargeControlMode{
		compatibility.ChargeControlLegacy,
		compatibility.ChargeControlFirmware,
		compatibility.ChargeControlNative,
		compatibility.ChargeControlUnsupported,
	} {
		if _, ok := f.d.selectCharger(mode).(chargeKeySwitch); !ok {
			t.Fatalf("%s must use the charge-key switch", mode)
		}
	}
}

func TestAdapterSwitchUsesSleepPolicy(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withCapabilities(adapterCaps()))
	f.mem.SetPreventSleepOnAdapterDisable(true)

	switcher := f.d.selectCharger(compatibility.ChargeControlAdapter)
	if err := switcher.Disable(); err != nil {
		t.Fatal(err)
	}
	if f.smc.adapterEnabled(t) || !f.sleep.disabled() || !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("adapter mode must hold sleep before cutting wall power")
	}
	if err := switcher.Enable(); err != nil {
		t.Fatal(err)
	}
	if !f.smc.adapterEnabled(t) || f.sleep.disabled() || f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("adapter mode must restore wall power before releasing the sleep hold")
	}
}

func TestAdapterSwitchDoesNotCutPowerIfSleepHoldFails(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withCapabilities(adapterCaps()))
	f.mem.SetPreventSleepOnAdapterDisable(true)
	f.sleep.setErr = errors.New("sleep setting unavailable")

	if err := (adapterSwitch{f.d}).Disable(); err == nil {
		t.Fatal("expected sleep hold failure")
	}
	if !f.smc.adapterEnabled(t) {
		t.Fatal("wall power must remain on when the sleep hold fails")
	}
}

func TestAdapterModeReconcilesSleepAfterRestart(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withCapabilities(adapterCaps()), withAdapter(false))
	f.mem.SetPreventSleepOnAdapterDisable(true)

	if err := f.d.reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if f.smc.adapterEnabled(t) || !f.sleep.disabled() || !f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("adapter mode must recover sleep protection for an already-cut adapter")
	}
	if err := f.d.shutdownAdapterAndSleep(); err != nil {
		t.Fatal(err)
	}
	if !f.smc.adapterEnabled(t) || f.sleep.disabled() || f.holdsSleep(sleepHoldAdapter) {
		t.Fatal("shutdown must restore wall power and sleep in adapter mode")
	}
}

func TestUsesActiveChargeControl(t *testing.T) {
	f := newFixture(t, firmwareLegacy)
	for mode, want := range map[compatibility.ChargeControlMode]bool{
		compatibility.ChargeControlLegacy:      true,
		compatibility.ChargeControlAdapter:     true,
		compatibility.ChargeControlFirmware:    false,
		compatibility.ChargeControlNative:      false,
		compatibility.ChargeControlUnsupported: false,
	} {
		f.setCaps(compatibility.Capabilities{ChargeControlMode: mode})
		if got := f.d.usesActiveChargeControl(); got != want {
			t.Errorf("usesActiveChargeControl(%s) = %v, want %v", mode, got, want)
		}
	}
}

func TestAdapterLoopChargesBelowLowerLimit(t *testing.T) {
	f := newAdapterLoopFixture(t, 50, true, false) // 50% <= lower, adapter currently cut
	fake := &fakeCharger{enabled: false}
	f.useCharger(fake)

	if !f.d.maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake.enables != 1 || !fake.enabled {
		t.Fatalf("below lower limit must restore wall power: %+v", fake)
	}
}

func TestAdapterLoopCutsAboveUpperLimit(t *testing.T) {
	f := newAdapterLoopFixture(t, 65, true, true) // 65% >= upper, adapter currently on
	fake := &fakeCharger{enabled: true}
	f.useCharger(fake)

	if !f.d.maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake.disables != 1 || fake.enabled {
		t.Fatalf("above upper limit must cut wall power: %+v", fake)
	}
}

func TestAdapterLoopHoldsWithinBand(t *testing.T) {
	f := newAdapterLoopFixture(t, 57, true, true) // between limits: leave as-is
	fake := &fakeCharger{enabled: true}
	f.useCharger(fake)

	if !f.d.maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake.enables != 0 || fake.disables != 0 {
		t.Fatalf("within band must not toggle the adapter: %+v", fake)
	}
}

func TestAdapterLoopReportsSwitchError(t *testing.T) {
	f := newAdapterLoopFixture(t, 50, true, false)
	f.useCharger(&fakeCharger{err: errors.New("smc gated")})

	if f.d.maintainActiveCharging(true) {
		t.Fatal("loop must fail when the charge switch errors")
	}
}

func TestAdapterLoopSkipsWhenUnplugged(t *testing.T) {
	f := newAdapterLoopFixture(t, 60, false, true) // 60% (above upper) but ON BATTERY, adapter "on"
	f.conf.SetUpperLimit(50)
	f.conf.SetLowerLimit(48)
	fake := &fakeCharger{enabled: true}
	f.useCharger(fake)

	if !f.d.maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake.enables != 0 || fake.disables != 0 {
		t.Fatalf("unplugged adapter mode must not toggle the adapter: %+v", fake)
	}
}

func TestAdapterLoopWidensNarrowBand(t *testing.T) {
	f := newAdapterLoopFixture(t, 46, true, false)
	f.conf.SetUpperLimit(50)
	f.conf.SetLowerLimit(48) // 2% band; adapterMinBand=5 should widen the floor to 45

	// At 46% (below the configured 48 but within the widened 45..50 band) the
	// adapter must NOT be restored — proving the band was widened.
	fake := &fakeCharger{enabled: false}
	f.useCharger(fake)
	if !f.d.maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake.enables != 0 {
		t.Fatalf("46%% is within the widened band; must not recharge yet: %+v", fake)
	}

	// At 44% (below the widened floor of 45) it must restore wall power.
	f.smc.setCharge(t, 44)
	fake2 := &fakeCharger{enabled: false}
	f.useCharger(fake2)
	if !f.d.maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake2.enables != 1 {
		t.Fatalf("44%% is below the widened floor; must recharge: %+v", fake2)
	}
}

func TestAdapterLoopRespectsWiderUserBand(t *testing.T) {
	f := newAdapterLoopFixture(t, 40, true, false)
	f.conf.SetUpperLimit(50)
	f.conf.SetLowerLimit(38) // 12% band, wider than adapterMinBand — must be respected

	// At 40% (below 45 but above the user's 38) the adapter must NOT recharge:
	// the min-band floor only widens a too-narrow band, never narrows a wide one.
	fake := &fakeCharger{enabled: false}
	f.useCharger(fake)
	if !f.d.maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake.enables != 0 {
		t.Fatalf("40%% is within the user's wide band; must not recharge: %+v", fake)
	}
}

// TestChargeControlRace exercises runtime adapter-mode changes concurrently with
// the readers used by the sleep callbacks and maintain loop. It must stay clean
// under `go test -race`.
func TestChargeControlRace(t *testing.T) {
	f := newFixture(t, firmwareLegacy)

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = f.d.usesActiveChargeControl()
					caps, ch := f.d.loadChargeControl()
					_ = caps.ChargeControlMode
					_ = ch
				}
			}
		}()
	}

	modes := []compatibility.ChargeControlMode{
		compatibility.ChargeControlAdapter,
		compatibility.ChargeControlNative,
		compatibility.ChargeControlLegacy,
	}
	for i := 0; i < 3000; i++ {
		f.d.setChargeControl(compatibility.Capabilities{ChargeControlMode: modes[i%len(modes)]})
	}
	close(stop)
	readers.Wait()
}

// TestReapplyChargeControlModeRace drives the real runtime toggle path
// (reapplyChargeControlMode, as the adapter-mode subcommand does) against the
// readers the sleep callbacks use. This is the scenario reported in #158 and
// must stay clean under `go test -race`.
func TestReapplyChargeControlModeRace(t *testing.T) {
	logrus.SetLevel(logrus.PanicLevel)
	t.Cleanup(func() { logrus.SetLevel(logrus.InfoLevel) })

	// macOS 27 firmware: adapter works, charge keys gated. The adapter loop
	// also reads the charge and the plug state, so the switch can succeed.
	f := newFixture(t, firmwareGated, withFileConfig(), withCharge(78),
		withNative(sliderNative(80, 85, 90, 95, 100)))
	f.file.SetUpperLimit(80)
	f.file.SetLowerLimit(75)

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = f.d.usesActiveChargeControl()
					caps, ch := f.d.loadChargeControl()
					_ = caps.ChargeControlMode
					_ = ch
				}
			}
		}()
	}

	for i := 0; i < 300; i++ {
		adapter := i%2 == 0
		f.file.SetAdapterMode(adapter) // flip between adapter and native
		want := compatibility.ChargeControlNative
		if adapter {
			want = compatibility.ChargeControlAdapter
		}
		err := f.d.reapplyChargeControlMode()
		if got := f.caps().ChargeControlMode; err != nil || got != want {
			close(stop)
			readers.Wait()
			t.Fatalf("reapplyChargeControlMode() = %v, mode %s, want nil and %s", err, got, want)
		}
	}
	close(stop)
	readers.Wait()
}
