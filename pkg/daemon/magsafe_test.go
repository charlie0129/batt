package daemon

import (
	"errors"
	"net/http"
	"testing"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

// newManagedMagSafeFixture is a MagSafe Mac in an Apple-managed charge-control
// mode with the LED initially in state initial and batt's LED control set to
// mode.
func newManagedMagSafeFixture(t *testing.T, controlMode compatibility.ChargeControlMode, initial smc.MagSafeLedState, mode config.ControlMagSafeMode, upper int) *fixture {
	t.Helper()
	f := newFixture(t, firmwareManaged, withFileConfig(), withLED(initial),
		withNative(&fakeNativeLimit{supported: true, exact: true, limit: 100}),
		withCapabilities(compatibility.Capabilities{
			ChargingControl: true, ChargeControlMode: controlMode, MagSafeLED: true,
			NativeExactTargets: controlMode == compatibility.ChargeControlNative,
		}))
	f.file.SetUpperLimit(upper)
	f.file.SetLowerLimit(upper - 2)
	f.file.SetControlMagSafeLED(mode)
	return f
}

func TestManagedModesUpdateMagSafeLED(t *testing.T) {
	tests := []struct {
		name     string
		mode     config.ControlMagSafeMode
		charging bool
		stateErr error
		initial  smc.MagSafeLedState
		want     smc.MagSafeLedState
		upper    int
		wantPoll bool
	}{
		{name: "charging is orange", mode: config.ControlMagSafeModeEnabled, charging: true, initial: smc.LEDGreen, want: smc.LEDOrange, upper: 70, wantPoll: true},
		{name: "charging takes over system LED control", mode: config.ControlMagSafeModeEnabled, charging: true, initial: smc.LEDSystem, want: smc.LEDOrange, upper: 70, wantPoll: true},
		{name: "not charging is green", mode: config.ControlMagSafeModeEnabled, initial: smc.LEDOrange, want: smc.LEDGreen, upper: 70, wantPoll: true},
		{name: "always off", mode: config.ControlMagSafeModeAlwaysOff, initial: smc.LEDOrange, want: smc.LEDOff, upper: 70},
		{name: "disabled leaves LED alone", mode: config.ControlMagSafeModeDisabled, initial: smc.LEDOrange, want: smc.LEDOrange, upper: 70},
		{name: "unavailable charging state leaves LED alone", mode: config.ControlMagSafeModeEnabled, stateErr: errors.New("unavailable"), initial: smc.LEDOrange, want: smc.LEDOrange, upper: 70, wantPoll: true},
		{name: "LED still follows charging with limit disabled", mode: config.ControlMagSafeModeEnabled, initial: smc.LEDOrange, want: smc.LEDGreen, upper: 100, wantPoll: true},
	}
	for _, controlMode := range []compatibility.ChargeControlMode{compatibility.ChargeControlFirmware, compatibility.ChargeControlNative} {
		for _, tt := range tests {
			t.Run(string(controlMode)+"/"+tt.name, func(t *testing.T) {
				f := newManagedMagSafeFixture(t, controlMode, tt.initial, tt.mode, tt.upper)
				f.battery.setCharging(tt.charging)
				f.battery.setErr(tt.stateErr)

				if !f.d.maintainLoopForced() {
					t.Fatalf("%s charge limit failed", controlMode)
				}
				if led := f.smc.led(t); led != tt.want {
					t.Fatalf("LED = %02x, want %02x", byte(led), byte(tt.want))
				}
				if polled := f.battery.pollCount() > 0; polled != tt.wantPoll {
					t.Fatalf("charging state queried = %t, want %t", polled, tt.wantPoll)
				}
				if tt.upper >= 100 {
					return
				}
				if controlMode == compatibility.ChargeControlNative {
					if !f.native.enabled || f.native.limit != tt.upper {
						t.Fatalf("unexpected native charge limit: %+v", f.native)
					}
					return
				}
				if limit := f.smc.firmwareLimit(t); !limit.Active || limit.Lower != 68 || limit.Upper != 70 {
					t.Fatalf("unexpected firmware charge limit: %+v", limit)
				}
			})
		}
	}
}

func TestManagedDisableMagSafeLEDReturnsControlToSystem(t *testing.T) {
	for _, controlMode := range []compatibility.ChargeControlMode{compatibility.ChargeControlFirmware, compatibility.ChargeControlNative} {
		t.Run(string(controlMode), func(t *testing.T) {
			f := newManagedMagSafeFixture(t, controlMode, smc.LEDGreen, config.ControlMagSafeModeEnabled, 70)
			f.mustServe(http.MethodPut, "/magsafe-led", `"disabled"`, http.StatusCreated)
			if led := f.smc.led(t); led != smc.LEDSystem {
				t.Fatalf("LED = %02x, want system control", byte(led))
			}
			if f.file.ControlMagSafeLED() != config.ControlMagSafeModeDisabled {
				t.Fatalf("mode = %s, want disabled", f.file.ControlMagSafeLED())
			}
		})
	}
}

func TestReleaseMagSafeLEDOnExit(t *testing.T) {
	tests := []struct {
		name string
		mode config.ControlMagSafeMode
		want smc.MagSafeLedState
	}{
		{name: "enabled returns control to system", mode: config.ControlMagSafeModeEnabled, want: smc.LEDSystem},
		{name: "always off returns control to system", mode: config.ControlMagSafeModeAlwaysOff, want: smc.LEDSystem},
		{name: "disabled leaves LED alone", mode: config.ControlMagSafeModeDisabled, want: smc.LEDOrange},
	}
	for _, controlMode := range []compatibility.ChargeControlMode{compatibility.ChargeControlFirmware, compatibility.ChargeControlNative} {
		for _, tt := range tests {
			t.Run(string(controlMode)+"/"+tt.name, func(t *testing.T) {
				f := newManagedMagSafeFixture(t, controlMode, smc.LEDOrange, tt.mode, 70)
				if err := f.d.releaseMagSafeLEDOnExit(); err != nil {
					t.Fatal(err)
				}
				if led := f.smc.led(t); led != tt.want {
					t.Fatalf("LED = %02x, want %02x", byte(led), byte(tt.want))
				}
			})
		}
	}
}

func TestLegacyLoopDrivesMagSafeLED(t *testing.T) {
	// In legacy mode the LED follows batt's own charge switch: orange while
	// batt lets the battery charge, green while it holds it.
	tests := []struct {
		name     string
		charge   int
		charging bool
		want     smc.MagSafeLedState
	}{
		{name: "below the band charges, orange", charge: 70, charging: false, want: smc.LEDOrange},
		{name: "above the limit holds, green", charge: 85, charging: true, want: smc.LEDGreen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, firmwareLegacy, withCharge(tt.charge), withCharging(tt.charging), withLED(smc.LEDSystem))
			f.mem.SetUpperLimit(80)
			f.mem.SetLowerLimit(75)
			f.mem.SetControlMagSafeLED(config.ControlMagSafeModeEnabled)

			if !f.d.maintainLoopForced() {
				t.Fatal("legacy maintain loop failed")
			}
			if led := f.smc.led(t); led != tt.want {
				t.Fatalf("LED = %02x, want %02x", byte(led), byte(tt.want))
			}
		})
	}
}
