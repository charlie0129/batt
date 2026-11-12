package daemon

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

func TestUnsupportedFeatureRejectedByDaemon(t *testing.T) {
	f := newFixture(t, firmwareManaged, withCapabilities(compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlFirmware,
	}))

	recorder := f.put("/prevent-idle-sleep", "true")
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
}

func TestDetectCapabilitiesForFirmwareControl(t *testing.T) {
	for _, withLED := range []bool{false, true} {
		t.Run(fmt.Sprintf("LED present=%t", withLED), func(t *testing.T) {
			f := newFixture(t, firmwareManaged.withMagSafe(withLED))
			got := f.d.detectCapabilities()
			if !got.ChargingControl || got.ChargeControlMode != compatibility.ChargeControlFirmware {
				t.Fatalf("unexpected charge control capability: %+v", got)
			}
			if got.SleepHooks || got.MagSafeLED != withLED || !got.AdapterControl || !got.Calibration {
				t.Fatalf("unexpected firmware-only capabilities: %+v", got)
			}
		})
	}
}

// TestDetectCapabilitiesMatrix pins the capabilities every kind of Mac gets,
// across the adapter-mode setting and the native limit macOS offers.
func TestDetectCapabilitiesMatrix(t *testing.T) {
	type expectation struct {
		mode                           compatibility.ChargeControlMode
		sleepHooks, magSafe, adapter   bool
		calibration, exact, lowerLimit bool
		supportedLimits                int
	}
	tests := []struct {
		name        string
		fw          firmware
		adapterMode bool
		native      *fakeNativeLimit
		want        expectation
	}{
		{
			name: "legacy",
			fw:   firmwareLegacy,
			want: expectation{mode: compatibility.ChargeControlLegacy, sleepHooks: true, magSafe: true, adapter: true, calibration: true, lowerLimit: true},
		},
		{
			name: "legacy without MagSafe",
			fw:   firmwareLegacy.withMagSafe(false),
			want: expectation{mode: compatibility.ChargeControlLegacy, sleepHooks: true, adapter: true, calibration: true, lowerLimit: true},
		},
		{
			name: "tahoe",
			fw:   firmwareTahoe,
			want: expectation{mode: compatibility.ChargeControlLegacy, sleepHooks: true, magSafe: true, adapter: true, calibration: true, lowerLimit: true},
		},
		{
			// The adapter-mode setting only matters once the charge keys are
			// gated; a Mac with working charge keys keeps the legacy loop.
			name:        "legacy ignores adapter mode",
			fw:          firmwareLegacy,
			adapterMode: true,
			want:        expectation{mode: compatibility.ChargeControlLegacy, sleepHooks: true, magSafe: true, adapter: true, calibration: true, lowerLimit: true},
		},
		{
			name: "firmware-managed",
			fw:   firmwareManaged,
			want: expectation{mode: compatibility.ChargeControlFirmware, magSafe: true, adapter: true, calibration: true, lowerLimit: true},
		},
		{
			// bfF0 wins over the legacy keys when a firmware exposes both.
			name: "firmware-managed with legacy keys",
			fw:   firmware{name: "both", chargeKeys: append(append([]string{}, firmwareManaged.chargeKeys...), firmwareLegacy.chargeKeys...), adapterKey: smc.AdapterKey1, magSafe: true},
			want: expectation{mode: compatibility.ChargeControlFirmware, magSafe: true, adapter: true, calibration: true, lowerLimit: true},
		},
		{
			name:   "gated, native limit offered",
			fw:     firmwareGated,
			native: sliderNative(80, 90, 100),
			want:   expectation{mode: compatibility.ChargeControlNative, adapter: true, calibration: true, supportedLimits: 3},
		},
		{
			name:   "gated with MagSafe, native limit offered",
			fw:     firmwareGated.withMagSafe(true),
			native: sliderNative(80, 90, 100),
			want:   expectation{mode: compatibility.ChargeControlNative, magSafe: true, adapter: true, calibration: true, supportedLimits: 3},
		},
		{
			name:   "gated, exact native targets",
			fw:     firmwareGated,
			native: exactNative(),
			want:   expectation{mode: compatibility.ChargeControlNative, adapter: true, calibration: true, exact: true, supportedLimits: 91, lowerLimit: true},
		},
		{
			name:        "gated, adapter mode opted in",
			fw:          firmwareGated,
			adapterMode: true,
			native:      sliderNative(80, 90, 100),
			want:        expectation{mode: compatibility.ChargeControlAdapter, sleepHooks: true, lowerLimit: true},
		},
		{
			name:        "gated with MagSafe, adapter mode opted in",
			fw:          firmwareGated.withMagSafe(true),
			adapterMode: true,
			want:        expectation{mode: compatibility.ChargeControlAdapter, sleepHooks: true, magSafe: true, lowerLimit: true},
		},
		{
			name: "gated, no native limit",
			fw:   firmwareGated,
			want: expectation{mode: compatibility.ChargeControlUnsupported, adapter: true},
		},
		{
			name:   "gated without adapter key, native limit offered",
			fw:     firmwareGatedNoAdapter,
			native: sliderNative(80, 100),
			want:   expectation{mode: compatibility.ChargeControlNative, supportedLimits: 2},
		},
		{
			// Without an adapter key the adapter-mode setting cannot be honored.
			name:        "gated without adapter key, adapter mode opted in",
			fw:          firmwareGatedNoAdapter,
			adapterMode: true,
			native:      sliderNative(80, 100),
			want:        expectation{mode: compatibility.ChargeControlNative, supportedLimits: 2},
		},
		{
			name: "nothing usable",
			fw:   firmwareGatedNoAdapter.withMagSafe(true),
			want: expectation{mode: compatibility.ChargeControlUnsupported},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := []fixtureOption{withoutDetection()}
			if tt.native != nil {
				opts = append(opts, withNative(tt.native))
			}
			f := newFixture(t, tt.fw, opts...)
			f.mem.SetAdapterMode(tt.adapterMode)

			got := f.d.detectCapabilities()
			want := tt.want
			if got.ChargeControlMode != want.mode {
				t.Fatalf("mode = %s, want %s (%+v)", got.ChargeControlMode, want.mode, got)
			}
			if got.ChargingControl != (want.mode != compatibility.ChargeControlUnsupported) {
				t.Errorf("ChargingControl = %t for mode %s", got.ChargingControl, got.ChargeControlMode)
			}
			if got.SleepHooks != want.sleepHooks || got.MagSafeLED != want.magSafe || got.AdapterControl != want.adapter ||
				got.Calibration != want.calibration || got.NativeExactTargets != want.exact {
				t.Errorf("capabilities = %+v, want sleepHooks=%t magSafe=%t adapter=%t calibration=%t exact=%t",
					got, want.sleepHooks, want.magSafe, want.adapter, want.calibration, want.exact)
			}
			if len(got.SupportedLimits) != want.supportedLimits {
				t.Errorf("SupportedLimits = %v, want %d values", got.SupportedLimits, want.supportedLimits)
			}
			if got.Supports(compatibility.FeatureLowerLimit) != want.lowerLimit {
				t.Errorf("lower limit supported = %t, want %t", got.Supports(compatibility.FeatureLowerLimit), want.lowerLimit)
			}
			// The charge switch always matches the mode.
			f.setCaps(got)
			_, isAdapter := f.d.getCharger().(adapterSwitch)
			if isAdapter != (got.ChargeControlMode == compatibility.ChargeControlAdapter) {
				t.Errorf("adapter switch selected = %t for mode %s", isAdapter, got.ChargeControlMode)
			}
		})
	}
}

func TestDisableUnsupportedConfiguredFeatures(t *testing.T) {
	f := newFixture(t, firmwareManaged, withFileConfig(), withCapabilities(compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlFirmware,
	}))
	f.file.SetPreventIdleSleep(true)
	f.file.SetDisableChargingPreSleep(true)
	f.file.SetPreventSystemSleep(true)
	f.file.SetControlMagSafeLED(config.ControlMagSafeModeEnabled)
	f.file.SetCron("0 10 * * 0")

	f.d.disableUnsupportedConfiguredFeatures()

	if f.file.PreventIdleSleep() || f.file.DisableChargingPreSleep() || f.file.PreventSystemSleep() {
		t.Fatal("sleep settings were not disabled")
	}
	if f.file.ControlMagSafeLED() != config.ControlMagSafeModeDisabled {
		t.Fatal("MagSafe LED control was not disabled")
	}
	if f.file.Cron() != "" {
		t.Fatal("calibration schedule was not disabled")
	}

	reloaded := f.reloadFile()
	if reloaded.PreventIdleSleep() || reloaded.DisableChargingPreSleep() || reloaded.PreventSystemSleep() || reloaded.Cron() != "" {
		t.Fatal("disabled settings were not persisted")
	}
}

func TestNativeModeDropsUnsupportedOneTimeTarget(t *testing.T) {
	f := newFixture(t, firmwareGatedNoAdapter, withFileConfig(), withCapabilities(nativeCaps(80, 85, 90, 95, 100)))
	f.file.SetChargeOnceTarget(80)
	if err := f.file.Save(); err != nil {
		t.Fatal(err)
	}

	f.d.disableUnsupportedConfiguredFeatures()
	if f.file.ChargeOnceTarget() != 0 {
		t.Fatal("native mode cannot force a charge to a target below 100%")
	}
	if reloaded := f.reloadFile(); reloaded.ChargeOnceTarget() != 0 {
		t.Fatal("unsupported target must be cleared on disk")
	}
}

func TestDisableUnsupportedCalibrationRestoresLimits(t *testing.T) {
	f := newFixture(t, firmwareManaged, withFileConfig(),
		withCapabilities(compatibility.Capabilities{ChargeControlMode: compatibility.ChargeControlFirmware}))
	f.file.SetUpperLimit(100)
	f.file.SetLowerLimit(98)
	f.setCalibration(&calibration.State{
		Phase:              calibration.PhaseCharge,
		SnapshotUpperLimit: 80,
		SnapshotLowerLimit: 78,
	})

	f.d.disableUnsupportedCalibrationState()
	if f.file.UpperLimit() != 80 || f.file.LowerLimit() != 78 {
		t.Fatalf("limits = %d/%d, want 80/78", f.file.UpperLimit(), f.file.LowerLimit())
	}
	if f.phase() != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", f.phase())
	}
}
