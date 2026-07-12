package daemon

import (
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

func TestUnsupportedFeatureRejectedByDaemon(t *testing.T) {
	previous := capabilities
	t.Cleanup(func() { capabilities = previous })
	capabilities = compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlFirmware,
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/prevent-idle-sleep", strings.NewReader("true"))
	setupRoutes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
}

func TestDetectCapabilitiesForFirmwareControl(t *testing.T) {
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
		value(smc.MagSafeLedKey, gosmc.TypeUInt8, 0),
	)
	if err := mock.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mock.Close() })

	previous := smcConn
	t.Cleanup(func() { smcConn = previous })
	smcConn = mock
	got := detectCapabilities()
	if !got.ChargingControl || got.ChargeControlMode != compatibility.ChargeControlFirmware {
		t.Fatalf("unexpected charge control capability: %+v", got)
	}
	if got.SleepHooks || got.MagSafeLED || got.AdapterControl || got.Calibration {
		t.Fatalf("unexpected firmware-only capabilities: %+v", got)
	}
}

func TestDisableUnsupportedConfiguredFeatures(t *testing.T) {
	path := t.TempDir() + "/batt.json"
	file, err := config.NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file.SetPreventIdleSleep(true)
	file.SetDisableChargingPreSleep(true)
	file.SetPreventSystemSleep(true)
	file.SetControlMagSafeLED(config.ControlMagSafeModeEnabled)
	file.SetCron("0 10 * * 0")

	previousConf, previousCapabilities := conf, capabilities
	t.Cleanup(func() { conf, capabilities = previousConf, previousCapabilities })
	conf = file
	capabilities = compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlFirmware,
	}
	disableUnsupportedConfiguredFeatures()

	if file.PreventIdleSleep() || file.DisableChargingPreSleep() || file.PreventSystemSleep() {
		t.Fatal("sleep settings were not disabled")
	}
	if file.ControlMagSafeLED() != config.ControlMagSafeModeDisabled {
		t.Fatal("MagSafe LED control was not disabled")
	}
	if file.Cron() != "" {
		t.Fatal("calibration schedule was not disabled")
	}

	reloaded, err := config.NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.PreventIdleSleep() || reloaded.DisableChargingPreSleep() || reloaded.PreventSystemSleep() || reloaded.Cron() != "" {
		t.Fatal("disabled settings were not persisted")
	}
}

func TestDisableUnsupportedCalibrationRestoresLimits(t *testing.T) {
	path := t.TempDir() + "/batt.json"
	file, err := config.NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file.SetUpperLimit(100)
	file.SetLowerLimit(98)

	previousConf, previousCapabilities := conf, capabilities
	previousState, previousStatePath := calibrationState, calibrationStatePath
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState, calibrationStatePath = previousState, previousStatePath
	})
	conf = file
	capabilities = compatibility.Capabilities{ChargeControlMode: compatibility.ChargeControlFirmware}
	calibrationStatePath = ""
	calibrationState = &calibration.State{
		Phase:              calibration.PhaseCharge,
		SnapshotUpperLimit: 80,
		SnapshotLowerLimit: 78,
	}

	disableUnsupportedCalibrationState()
	if file.UpperLimit() != 80 || file.LowerLimit() != 78 {
		t.Fatalf("limits = %d/%d, want 80/78", file.UpperLimit(), file.LowerLimit())
	}
	if calibrationState.Phase != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", calibrationState.Phase)
	}
}
