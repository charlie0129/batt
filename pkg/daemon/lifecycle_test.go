package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

// These tests cover what Run does before it serves requests (start) and after
// it receives a signal (shutdown), on each kind of Mac.

func TestStartDetectsCapabilitiesPerFirmware(t *testing.T) {
	tests := []struct {
		name   string
		fw     firmware
		native *fakeNativeLimit
		want   compatibility.ChargeControlMode
	}{
		{name: "legacy", fw: firmwareLegacy, want: compatibility.ChargeControlLegacy},
		{name: "tahoe", fw: firmwareTahoe, want: compatibility.ChargeControlLegacy},
		{name: "firmware-managed", fw: firmwareManaged, want: compatibility.ChargeControlFirmware},
		{name: "gated with native limit", fw: firmwareGated, native: sliderNative(80, 100), want: compatibility.ChargeControlNative},
		{name: "gated without native limit", fw: firmwareGated, want: compatibility.ChargeControlUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := []fixtureOption{withoutDetection()}
			if tt.native != nil {
				opts = append(opts, withNative(tt.native))
			}
			f := newFixture(t, tt.fw, opts...)
			if err := f.d.start(f.dir); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(f.d.scheduler.Stop)
			if got := f.caps().ChargeControlMode; got != tt.want {
				t.Fatalf("mode = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestStartDropsSettingsTheMacCannotHonor(t *testing.T) {
	// A config written on a legacy Mac is carried to a firmware-managed one:
	// the sleep hooks and the calibration schedule no longer apply.
	f := newFixture(t, firmwareManaged, withFileConfig(), withoutDetection())
	f.file.SetPreventIdleSleep(true)
	f.file.SetDisableChargingPreSleep(true)
	f.file.SetCron("0 10 * * 0")
	if err := f.file.Save(); err != nil {
		t.Fatal(err)
	}

	if err := f.d.start(f.dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.d.scheduler.Stop)

	reloaded := f.reloadFile()
	if reloaded.PreventIdleSleep() || reloaded.DisableChargingPreSleep() {
		t.Fatal("sleep hooks must be switched off on a Mac without them")
	}
	if reloaded.Cron() == "" {
		t.Fatal("the firmware-managed Mac supports calibration, so the schedule must stay")
	}
	if _, running := f.d.scheduler.Status(); !running {
		t.Fatal("the persisted schedule must be restored")
	}
}

func TestStartRequiresRootForExactNativeTargets(t *testing.T) {
	f := newFixture(t, firmwareGated, withNative(exactNative()), withoutDetection())
	f.d.euid = func() int { return 501 }

	if err := f.d.start(f.dir); err == nil {
		t.Fatal("exact native targets need the root daemon")
	}
}

func TestStartRestoresTheSleepSettingAPreviousRunLeftBehind(t *testing.T) {
	// The previous daemon died with SleepDisabled on and the adapter already
	// restored. Nothing holds sleep now, so the user's setting comes back.
	f := newFixture(t, firmwareLegacy, withSleepDisabled(true), withoutDetection())
	path := filepath.Join(f.dir, "batt.sleep.json")
	if err := os.WriteFile(path, []byte(`{"previous":false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := f.d.start(f.dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.d.scheduler.Stop)

	if f.sleep.disabled() {
		t.Fatal("the user's SleepDisabled=false must be restored")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the consumed snapshot must be removed")
	}
}

func TestStartKeepsSleepHeldWhileTheAdapterIsStillCut(t *testing.T) {
	// Same crash, but the adapter is still cut and the setting wants Clamshell
	// mode protected: sleep must stay disabled without a gap.
	f := newFixture(t, firmwareLegacy, withSleepDisabled(true), withAdapter(false), withoutDetection())
	f.mem.SetPreventSleepOnAdapterDisable(true)
	path := filepath.Join(f.dir, "batt.sleep.json")
	if err := os.WriteFile(path, []byte(`{"previous":false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := f.d.start(f.dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.d.scheduler.Stop)

	if !f.sleep.disabled() || !f.holdsSleep(sleepHoldAdapter) {
		t.Fatalf("sleep protection must be retained: disabled=%t hold=%t", f.sleep.disabled(), f.holdsSleep(sleepHoldAdapter))
	}
	for _, value := range f.sleep.writtenValues {
		if !value {
			t.Fatal("startup briefly restored sleep before reacquiring the hold")
		}
	}
}

func TestStartResumesAPersistedCalibrationPaused(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withCharge(40), withCharging(false))
	if err := f.d.startCalibration(15, 10); err != nil {
		t.Fatal(err)
	}

	restarted := newFixture(t, firmwareLegacy, withConfig(f.mem), withCharge(40), withCharging(false), withoutDetection())
	if err := restarted.d.start(f.dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.d.scheduler.Stop)

	if st := restarted.calibration(); st.Phase != calibration.PhaseDischarge || !st.Paused {
		t.Fatalf("calibration = %+v, want discharge phase, paused", st)
	}
	if !restarted.asserts.holdsCalibrationSleep() {
		t.Fatal("the restored calibration must hold its sleep assertion")
	}
}

func TestShutdownHandsTheHardwareBack(t *testing.T) {
	t.Run("legacy", func(t *testing.T) {
		f := newFixture(t, firmwareLegacy, withCharge(85), withCharging(true), withLED(smc.LEDSystem), withAdapter(false))
		f.mem.SetControlMagSafeLED(config.ControlMagSafeModeEnabled)
		f.mem.SetPreventSleepOnAdapterDisable(true)
		if !f.d.maintainLoopForced() || f.smc.chargingEnabled(t) {
			t.Fatal("precondition: batt must have disabled charging above the limit")
		}
		if err := f.d.disableAdapter(); err != nil {
			t.Fatal(err)
		}

		if err := f.d.shutdown(); err != nil {
			t.Fatal(err)
		}

		if !f.smc.chargingEnabled(t) {
			t.Fatal("charging must be handed back to macOS")
		}
		if !f.smc.adapterEnabled(t) || f.sleep.disabled() || f.d.holds.count() != 0 {
			t.Fatal("wall power and the sleep setting must be restored")
		}
		if led := f.smc.led(t); led != smc.LEDSystem {
			t.Fatalf("LED = %02x, want system control", byte(led))
		}
		if f.smc.callCount("Close") != 1 {
			t.Fatal("the SMC connection must be closed")
		}
	})

	t.Run("adapter mode", func(t *testing.T) {
		f := newFixture(t, firmwareGated, withCapabilities(adapterCaps()), withCharge(85))
		f.mem.SetUpperLimit(80)
		f.mem.SetLowerLimit(75)
		if !f.d.maintainLoopForced() || f.smc.adapterEnabled(t) {
			t.Fatal("precondition: batt must have cut wall power above the limit")
		}

		if err := f.d.shutdown(); err != nil {
			t.Fatal(err)
		}
		if !f.smc.adapterEnabled(t) {
			t.Fatal("wall power must be restored so the Mac is not left on battery")
		}
	})

	t.Run("firmware-managed", func(t *testing.T) {
		f := newFixture(t, firmwareManaged)
		if !f.d.maintainLoopForced() || !f.smc.firmwareLimit(t).Active {
			t.Fatal("precondition: the firmware limit must be active")
		}

		if err := f.d.shutdown(); err != nil {
			t.Fatal(err)
		}
		if f.smc.firmwareLimit(t).Active {
			t.Fatal("the firmware limit must be deactivated")
		}
	})

	t.Run("native exact targets", func(t *testing.T) {
		f := newExactNativeFixture(t)
		f.file.SetUpperLimit(55)
		if !f.d.maintainLoopForced() || !f.native.enabled {
			t.Fatal("precondition: the native limit must be active")
		}

		if err := f.d.shutdown(); err != nil {
			t.Fatal(err)
		}
		if f.native.enabled {
			t.Fatal("the native limit must be released")
		}
		if f.d.maintainLoopForced(); f.native.enabled {
			t.Fatal("a queued maintenance pass must not reapply the limit after shutdown")
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		f := newFixture(t, firmwareGated)
		if f.caps().ChargingControl {
			t.Fatalf("precondition: charge control must be unsupported, got %+v", f.caps())
		}
		if err := f.d.shutdown(); err != nil {
			t.Fatal(err)
		}
		if f.smc.callCount("ResetChargeControl") != 0 || f.smc.callCount("EnableCharging") != 0 {
			t.Fatal("nothing to hand back without charge control")
		}
	})
}

func TestShutdownReportsWhatItCouldNotRestore(t *testing.T) {
	f := newFixture(t, firmwareLegacy, withCharge(85), withCharging(false))
	f.smc.fail("ResetChargeControl", errors.New("smc write failed"))

	err := f.d.shutdown()
	if err == nil {
		t.Fatal("a failed hand-back must be reported")
	}
	if f.smc.callCount("Close") != 1 {
		t.Fatal("the SMC connection must be closed even after a failure")
	}
}

func TestRunLoopStopsWhenCancelled(t *testing.T) {
	f := newFixture(t, firmwareLegacy)
	f.d.loopInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		f.d.runLoop(ctx)
		close(done)
	}()

	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the loop did not stop after cancellation")
	}
	if f.smc.callCount("GetBatteryCharge") < 2 {
		t.Fatalf("the loop ran %d times, want several", f.smc.callCount("GetBatteryCharge"))
	}
}

func TestStateDir(t *testing.T) {
	if got := stateDir(""); got != "/etc" {
		t.Fatalf("stateDir(\"\") = %q", got)
	}
	if got := stateDir("/Users/me/.batt/batt.json"); got != "/Users/me/.batt" {
		t.Fatalf("stateDir() = %q", got)
	}
}
