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

func firmwareMagSafeFixture(t *testing.T, initial byte, mode config.ControlMagSafeMode, upper int) *smc.AppleSMC {
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
	previousCalibration, previousIsCharging := calibrationState, firmwareIsCharging
	t.Cleanup(func() {
		smcConn, conf, capabilities = previousSMC, previousConf, previousCapabilities
		calibrationState, firmwareIsCharging = previousCalibration, previousIsCharging
	})
	smcConn, conf = mock, file
	capabilities = compatibility.Capabilities{
		ChargingControl: true, ChargeControlMode: compatibility.ChargeControlFirmware, MagSafeLED: true,
	}
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	return mock
}

func TestFirmwareModeUpdatesMagSafeLED(t *testing.T) {
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := firmwareMagSafeFixture(t, byte(tt.initial), tt.mode, tt.upper)
			polled := false
			firmwareIsCharging = func() (bool, error) {
				polled = true
				return tt.charging, tt.stateErr
			}

			if !maintainLoopForced() {
				t.Fatal("firmware charge limit failed")
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
			limit, err := mock.GetFirmwareChargeLimit()
			if err != nil {
				t.Fatal(err)
			}
			if tt.upper < 100 && (!limit.Active || limit.Lower != 68 || limit.Upper != 70) {
				t.Fatalf("unexpected firmware charge limit: %+v", limit)
			}
		})
	}
}

func TestFirmwareDisableMagSafeLEDReturnsControlToSystem(t *testing.T) {
	mock := firmwareMagSafeFixture(t, byte(smc.LEDGreen), config.ControlMagSafeModeEnabled, 70)
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
}
