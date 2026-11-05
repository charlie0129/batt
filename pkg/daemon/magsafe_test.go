package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charlie0129/gosmc"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

func managedMagSafeFixture(t *testing.T, controlMode compatibility.ChargeControlMode, initial byte, mode config.ControlMagSafeMode, upper int) (*smc.AppleSMC, *fakeNativeLimit) {
	t.Helper()
	value := func(key string, dataType gosmc.DataType, data ...byte) gosmc.Value {
		v, err := gosmc.NewValue(key, dataType, data)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	mock := smc.NewMockValues(
		value(smc.FirmwareChargeLimitActivationKey, gosmc.TypeUInt8, 0),
		value(smc.FirmwareChargeLimitUpperKey, gosmc.TypeUInt32, 0, 0, 0, 0),
		value(smc.FirmwareChargeLimitLowerKey, gosmc.TypeUInt32, 0, 0, 0, 0),
		value(smc.MagSafeLedKey, gosmc.TypeUInt8, initial),
	)
	if err := mock.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mock.Close() })

	file, err := config.NewFile(t.TempDir() + "/batt.json")
	if err != nil {
		t.Fatal(err)
	}
	file.SetUpperLimit(upper)
	file.SetLowerLimit(upper - 2)
	file.SetControlMagSafeLED(mode)

	previousSMC, previousConf, previousCapabilities := smcConn, conf, capabilities
	previousCalibration, previousIsCharging := calibrationState, systemIsCharging
	t.Cleanup(func() {
		smcConn, conf, capabilities = previousSMC, previousConf, previousCapabilities
		calibrationState, systemIsCharging = previousCalibration, previousIsCharging
	})
	smcConn, conf = mock, file
	capabilities = compatibility.Capabilities{
		ChargingControl: true, ChargeControlMode: controlMode, MagSafeLED: true,
		NativeExactTargets: controlMode == compatibility.ChargeControlNative,
	}
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	native := &fakeNativeLimit{supported: true, exact: true, limit: 100}
	useFakeNativeLimit(t, native)
	return mock, native
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
				mock, native := managedMagSafeFixture(t, controlMode, byte(tt.initial), tt.mode, tt.upper)
				polled := false
				systemIsCharging = func() (bool, error) {
					polled = true
					return tt.charging, tt.stateErr
				}

				if !maintainLoopForced() {
					t.Fatalf("%s charge limit failed", controlMode)
				}
				led, err := mock.Read(smc.MagSafeLedKey)
				if err != nil {
					t.Fatal(err)
				}
				if len(led.Bytes) != 1 || led.Bytes[0] != byte(tt.want) {
					t.Fatalf("LED = %x, want %02x", led.Bytes, tt.want)
				}
				if polled != tt.wantPoll {
					t.Fatalf("charging state queried = %t, want %t", polled, tt.wantPoll)
				}
				if tt.upper >= 100 {
					return
				}
				if controlMode == compatibility.ChargeControlNative {
					if !native.enabled || native.limit != tt.upper {
						t.Fatalf("unexpected native charge limit: %+v", native)
					}
					return
				}
				limit, err := mock.GetFirmwareChargeLimit()
				if err != nil {
					t.Fatal(err)
				}
				if !limit.Active || limit.Lower != 68 || limit.Upper != 70 {
					t.Fatalf("unexpected firmware charge limit: %+v", limit)
				}
			})
		}
	}
}

func TestManagedDisableMagSafeLEDReturnsControlToSystem(t *testing.T) {
	for _, controlMode := range []compatibility.ChargeControlMode{compatibility.ChargeControlFirmware, compatibility.ChargeControlNative} {
		t.Run(string(controlMode), func(t *testing.T) {
			mock, _ := managedMagSafeFixture(t, controlMode, byte(smc.LEDGreen), config.ControlMagSafeModeEnabled, 70)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/magsafe-led", strings.NewReader(`"disabled"`))
			setupRoutes().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusCreated {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			led, err := mock.Read(smc.MagSafeLedKey)
			if err != nil {
				t.Fatal(err)
			}
			if len(led.Bytes) != 1 || led.Bytes[0] != byte(smc.LEDSystem) {
				t.Fatalf("LED = %x, want system control", led.Bytes)
			}
			if conf.ControlMagSafeLED() != config.ControlMagSafeModeDisabled {
				t.Fatalf("mode = %s, want disabled", conf.ControlMagSafeLED())
			}
		})
	}
}
