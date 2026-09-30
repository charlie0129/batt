package daemon

import (
	"errors"
	"sync"
	"testing"

	"github.com/charlie0129/gosmc"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/smc"
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

func useCharger(t *testing.T, c chargeSwitch) {
	t.Helper()
	previous := charger
	t.Cleanup(func() { charger = previous })
	charger = c
}

func useAdapterCapabilities(t *testing.T) {
	t.Helper()
	previous := capabilities
	t.Cleanup(func() { capabilities = previous })
	capabilities = compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlAdapter,
		SleepHooks:        true,
	}
}

func TestSelectCharger(t *testing.T) {
	if _, ok := selectCharger(compatibility.ChargeControlAdapter).(adapterSwitch); !ok {
		t.Fatal("adapter mode must use the adapter switch")
	}
	for _, mode := range []compatibility.ChargeControlMode{
		compatibility.ChargeControlLegacy,
		compatibility.ChargeControlFirmware,
		compatibility.ChargeControlNative,
		compatibility.ChargeControlUnsupported,
	} {
		if _, ok := selectCharger(mode).(chargeKeySwitch); !ok {
			t.Fatalf("%s must use the charge-key switch", mode)
		}
	}
}

func TestAdapterSwitchUsesSleepPolicy(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, true)
	capabilities = compatibility.Capabilities{ChargeControlMode: compatibility.ChargeControlAdapter}
	conf = &sleepPolicyConf{prevent: true}

	switcher := selectCharger(compatibility.ChargeControlAdapter)
	if err := switcher.Disable(); err != nil {
		t.Fatal(err)
	}
	if adapter.enabled || !sleep.value || !sleepHolds[sleepHoldAdapter] {
		t.Fatal("adapter mode must hold sleep before cutting wall power")
	}
	if err := switcher.Enable(); err != nil {
		t.Fatal(err)
	}
	if !adapter.enabled || sleep.value || sleepHolds[sleepHoldAdapter] {
		t.Fatal("adapter mode must restore wall power before releasing the sleep hold")
	}
}

func TestAdapterSwitchDoesNotCutPowerIfSleepHoldFails(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, true)
	capabilities = compatibility.Capabilities{ChargeControlMode: compatibility.ChargeControlAdapter}
	conf = &sleepPolicyConf{prevent: true}
	sleep.setErr = errors.New("sleep setting unavailable")

	if err := (adapterSwitch{}).Disable(); err == nil {
		t.Fatal("expected sleep hold failure")
	}
	if !adapter.enabled {
		t.Fatal("wall power must remain on when the sleep hold fails")
	}
}

func TestAdapterModeReconcilesSleepAfterRestart(t *testing.T) {
	sleep := stubSleepDisabled(t, false)
	adapter := stubAdapter(t, false)
	capabilities = compatibility.Capabilities{ChargeControlMode: compatibility.ChargeControlAdapter}
	conf = &sleepPolicyConf{prevent: true}

	if err := reconcileAdapterSleepPolicy(); err != nil {
		t.Fatal(err)
	}
	if adapter.enabled || !sleep.value || !sleepHolds[sleepHoldAdapter] {
		t.Fatal("adapter mode must recover sleep protection for an already-cut adapter")
	}
	if err := shutdownAdapterAndSleep(); err != nil {
		t.Fatal(err)
	}
	if !adapter.enabled || sleep.value || sleepHolds[sleepHoldAdapter] {
		t.Fatal("shutdown must restore wall power and sleep in adapter mode")
	}
}

func TestUsesActiveChargeControl(t *testing.T) {
	previous := capabilities
	t.Cleanup(func() { capabilities = previous })
	for mode, want := range map[compatibility.ChargeControlMode]bool{
		compatibility.ChargeControlLegacy:      true,
		compatibility.ChargeControlAdapter:     true,
		compatibility.ChargeControlFirmware:    false,
		compatibility.ChargeControlNative:      false,
		compatibility.ChargeControlUnsupported: false,
	} {
		capabilities = compatibility.Capabilities{ChargeControlMode: mode}
		if got := usesActiveChargeControl(); got != want {
			t.Errorf("usesActiveChargeControl(%s) = %v, want %v", mode, got, want)
		}
	}
}

// adapterMockSMC builds an SMC mock that reports the given battery charge and a
// working adapter key, so the adapter hysteresis loop can run end to end.
func adapterMockSMC(t *testing.T, chargePercent int, pluggedIn, adapterOn bool) *smc.AppleSMC {
	t.Helper()
	adapterByte := byte(0) // 0 = enabled
	if !adapterOn {
		adapterByte = 8
	}
	ac := byte(0)
	if pluggedIn {
		ac = 1
	}
	vals := []gosmc.Value{
		mustValue(t, smc.AdapterKey3, gosmc.TypeUInt8, adapterByte),
		mustValue(t, smc.BatteryChargeKey, gosmc.TypeUInt8, byte(chargePercent)),
		mustValue(t, smc.ACPowerKey, gosmc.TypeSInt8, ac),
	}
	return useMockSMC(t, vals...)
}

func mustValue(t *testing.T, key string, dt gosmc.DataType, data ...byte) gosmc.Value {
	t.Helper()
	v, err := gosmc.NewValue(key, dt, data)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAdapterLoopChargesBelowLowerLimit(t *testing.T) {
	useAdapterCapabilities(t)
	file, _ := useTempConfig(t)
	file.SetUpperLimit(60)
	file.SetLowerLimit(55)
	adapterMockSMC(t, 50, true, false) // 50% <= lower, adapter currently cut
	fake := &fakeCharger{enabled: false}
	useCharger(t, fake)

	if !maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake.enables != 1 || !fake.enabled {
		t.Fatalf("below lower limit must restore wall power: %+v", fake)
	}
}

func TestAdapterLoopCutsAboveUpperLimit(t *testing.T) {
	useAdapterCapabilities(t)
	file, _ := useTempConfig(t)
	file.SetUpperLimit(60)
	file.SetLowerLimit(55)
	adapterMockSMC(t, 65, true, true) // 65% >= upper, adapter currently on
	fake := &fakeCharger{enabled: true}
	useCharger(t, fake)

	if !maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake.disables != 1 || fake.enabled {
		t.Fatalf("above upper limit must cut wall power: %+v", fake)
	}
}

func TestAdapterLoopHoldsWithinBand(t *testing.T) {
	useAdapterCapabilities(t)
	file, _ := useTempConfig(t)
	file.SetUpperLimit(60)
	file.SetLowerLimit(55)
	adapterMockSMC(t, 57, true, true) // between limits: leave as-is
	fake := &fakeCharger{enabled: true}
	useCharger(t, fake)

	if !maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake.enables != 0 || fake.disables != 0 {
		t.Fatalf("within band must not toggle the adapter: %+v", fake)
	}
}

func TestAdapterLoopReportsSwitchError(t *testing.T) {
	useAdapterCapabilities(t)
	file, _ := useTempConfig(t)
	file.SetUpperLimit(60)
	file.SetLowerLimit(55)
	adapterMockSMC(t, 50, true, false)
	useCharger(t, &fakeCharger{err: errors.New("smc gated")})

	if maintainActiveCharging(true) {
		t.Fatal("loop must fail when the charge switch errors")
	}
}

func TestAdapterLoopSkipsWhenUnplugged(t *testing.T) {
	useAdapterCapabilities(t)
	file, _ := useTempConfig(t)
	file.SetUpperLimit(50)
	file.SetLowerLimit(48)
	adapterMockSMC(t, 60, false, true) // 60% (above upper) but ON BATTERY, adapter "on"
	fake := &fakeCharger{enabled: true}
	useCharger(t, fake)

	if !maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake.enables != 0 || fake.disables != 0 {
		t.Fatalf("unplugged adapter mode must not toggle the adapter: %+v", fake)
	}
}

func TestAdapterLoopWidensNarrowBand(t *testing.T) {
	useAdapterCapabilities(t)
	file, _ := useTempConfig(t)
	file.SetUpperLimit(50)
	file.SetLowerLimit(48) // 2% band; adapterMinBand=5 should widen the floor to 45

	// At 46% (below the configured 48 but within the widened 45..50 band) the
	// adapter must NOT be restored — proving the band was widened.
	adapterMockSMC(t, 46, true, false)
	fake := &fakeCharger{enabled: false}
	useCharger(t, fake)
	if !maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake.enables != 0 {
		t.Fatalf("46%% is within the widened band; must not recharge yet: %+v", fake)
	}

	// At 44% (below the widened floor of 45) it must restore wall power.
	adapterMockSMC(t, 44, true, false)
	fake2 := &fakeCharger{enabled: false}
	useCharger(t, fake2)
	if !maintainActiveCharging(true) {
		t.Fatal("loop returned false")
	}
	if fake2.enables != 1 {
		t.Fatalf("44%% is below the widened floor; must recharge: %+v", fake2)
	}
}

func TestAdapterLoopRespectsWiderUserBand(t *testing.T) {
	useAdapterCapabilities(t)
	file, _ := useTempConfig(t)
	file.SetUpperLimit(50)
	file.SetLowerLimit(38) // 12% band, wider than adapterMinBand — must be respected

	// At 40% (below 45 but above the user's 38) the adapter must NOT recharge:
	// the min-band floor only widens a too-narrow band, never narrows a wide one.
	adapterMockSMC(t, 40, true, false)
	fake := &fakeCharger{enabled: false}
	useCharger(t, fake)
	if !maintainActiveCharging(true) {
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
	origCaps, origCharger := loadChargeControl()
	t.Cleanup(func() {
		capabilities = origCaps
		charger = origCharger
	})

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
					_ = usesActiveChargeControl()
					caps, ch := loadChargeControl()
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
		setChargeControl(compatibility.Capabilities{ChargeControlMode: modes[i%len(modes)]})
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
	adapterMockSMC(t, 78, true, true)
	file, _ := useTempConfig(t)
	file.SetUpperLimit(80)
	file.SetLowerLimit(75)
	useFakeNativeLimit(t, &fakeNativeLimit{supported: true, limits: []int{80, 85, 90, 95, 100}})

	origCaps, origCharger := loadChargeControl()
	t.Cleanup(func() {
		capabilities = origCaps
		charger = origCharger
	})

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
					_ = usesActiveChargeControl()
					caps, ch := loadChargeControl()
					_ = caps.ChargeControlMode
					_ = ch
				}
			}
		}()
	}

	for i := 0; i < 300; i++ {
		adapter := i%2 == 0
		file.SetAdapterMode(adapter) // flip between adapter and native
		want := compatibility.ChargeControlNative
		if adapter {
			want = compatibility.ChargeControlAdapter
		}
		err := reapplyChargeControlMode()
		if got := getCapabilities().ChargeControlMode; err != nil || got != want {
			close(stop)
			readers.Wait()
			t.Fatalf("reapplyChargeControlMode() = %v, mode %s, want nil and %s", err, got, want)
		}
	}
	close(stop)
	readers.Wait()
}
