package daemon

import (
	"testing"
	"time"

	"github.com/charlie0129/gosmc"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

// NOTE: These tests are simplified and mock minimal parts; smcConn and conf must be
// initialized externally for full integration. Here we focus on state transitions logic.

// mockConf implements the subset of Config used in calibration for test.
type mockConf struct {
	upper           int
	lower           int
	disableUntil    time.Time
	preDisableLimit int
}

func (m *mockConf) UpperLimit() int               { return m.upper }
func (m *mockConf) LowerLimit() int               { return m.lower }
func (m *mockConf) PreventIdleSleep() bool        { return false }
func (m *mockConf) DisableChargingPreSleep() bool { return false }
func (m *mockConf) PreventSystemSleep() bool      { return false }
func (m *mockConf) AllowNonRootAccess() bool      { return false }
func (m *mockConf) ControlMagSafeLED() config.ControlMagSafeMode {
	return config.ControlMagSafeModeDisabled
}
func (m *mockConf) CalibrationDischargeThreshold() int             { return 15 }
func (m *mockConf) CalibrationHoldDurationMinutes() int            { return 1 }
func (m *mockConf) SetCalibrationDischargeThreshold(int)           {}
func (m *mockConf) SetCalibrationHoldDurationMinutes(int)          {}
func (m *mockConf) SetUpperLimit(i int)                            { m.upper = i }
func (m *mockConf) SetLowerLimit(i int)                            { m.lower = i }
func (m *mockConf) SetPreventIdleSleep(bool)                       {}
func (m *mockConf) SetDisableChargingPreSleep(bool)                {}
func (m *mockConf) SetPreventSystemSleep(bool)                     {}
func (m *mockConf) SetAllowNonRootAccess(bool)                     {}
func (m *mockConf) SetControlMagSafeLED(config.ControlMagSafeMode) {}
func (m *mockConf) LogrusFields() logrus.Fields                    { return logrus.Fields{} }
func (m *mockConf) Load() error                                    { return nil }
func (m *mockConf) Save() error                                    { return nil }
func (m *mockConf) Cron() string                                   { return "" }
func (m *mockConf) SetCron(string)                                 {}
func (m *mockConf) DisableUntil() time.Time                        { return m.disableUntil }
func (m *mockConf) PreDisableLimit() int                           { return m.preDisableLimit }
func (m *mockConf) SetDisableTimer(until time.Time, prevLimit int) {
	m.disableUntil = until
	m.preDisableLimit = prevLimit
}
func (m *mockConf) ClearDisableTimer() {
	m.disableUntil = time.Time{}
	m.preDisableLimit = 0
}

// Fake smcConn implementation.
type fakeSMC struct {
	charge   int
	charging bool
	adapter  bool
}

func newFakeSMC(c, ch int, adapter bool) *fakeSMC {
	return &fakeSMC{charge: c, charging: ch != 0, adapter: adapter}
}

func (f *fakeSMC) inject() {
	smcGetBatteryCharge = func() (int, error) { return f.charge, nil }
	smcIsChargingEnabled = func() (bool, error) { return f.charging, nil }
	smcEnableCharging = func() error { f.charging = true; return nil }
	smcDisableCharging = func() error { f.charging = false; return nil }
	smcIsAdapterEnabled = func() (bool, error) { return f.adapter, nil }
	smcEnableAdapter = func() error { f.adapter = true; return nil }
	smcDisableAdapter = func() error { f.adapter = false; return nil }
	smcIsPluggedIn = func() (bool, error) { return f.adapter, nil }
}

type calibrationSleepCalls struct {
	prevent int
	allow   int
}

func stubCalibrationSleep(t *testing.T) *calibrationSleepCalls {
	t.Helper()
	previousPrevent, previousAllow := preventCalibrationSleep, allowCalibrationSleep
	t.Cleanup(func() {
		preventCalibrationSleep, allowCalibrationSleep = previousPrevent, previousAllow
	})
	calls := &calibrationSleepCalls{}
	preventCalibrationSleep = func() error {
		calls.prevent++
		return nil
	}
	allowCalibrationSleep = func() error {
		calls.allow++
		return nil
	}
	return calls
}

func TestStartCalibrationRejectsTemporaryDisable(t *testing.T) {
	previousConf, previousState, previousStatePath := conf, calibrationState, calibrationStatePath
	t.Cleanup(func() {
		conf, calibrationState, calibrationStatePath = previousConf, previousState, previousStatePath
	})

	sleepCalls := stubCalibrationSleep(t)
	conf = &mockConf{
		upper:           100,
		lower:           78,
		disableUntil:    time.Now().Add(time.Hour),
		preDisableLimit: 80,
	}
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	calibrationStatePath = ""

	if err := startCalibration(15, 10); err != ErrTemporaryDisableInProgress {
		t.Fatalf("startCalibration() error = %v, want %v", err, ErrTemporaryDisableInProgress)
	}
	if calibrationState.Phase != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", calibrationState.Phase)
	}
	if sleepCalls.prevent != 0 {
		t.Fatal("rejected calibration acquired a sleep assertion")
	}
}

// TestCalibrationFlow simulates the main phase transitions.
func TestCalibrationFlow(t *testing.T) {
	sleepCalls := stubCalibrationSleep(t)
	// Inject mocks
	fake := newFakeSMC(40, 0, true)
	fake.inject()
	conf = &mockConf{upper: 80, lower: 78}
	calibrationStatePath = "" // disable persistence

	if err := startCalibration(15, 1); err != nil {
		t.Fatalf("startCalibration failed: %v", err)
	}
	if calibrationState.Phase != calibration.PhaseDischarge {
		t.Fatalf("expected discharge phase, got %s", calibrationState.Phase)
	}
	if err := pauseCalibration(); err != nil {
		t.Fatal(err)
	}
	if sleepCalls.allow != 0 {
		t.Fatal("pausing calibration released its sleep assertion")
	}
	if err := resumeCalibration(); err != nil {
		t.Fatal(err)
	}

	// Move charge below threshold to trigger charging phase
	fake.charge = 14
	applyCalibrationWithinLoop(fake.charge)
	if calibrationState.Phase != calibration.PhaseCharge {
		t.Fatalf("expected charge phase, got %s", calibrationState.Phase)
	}
	if !fake.charging {
		t.Fatalf("expected charging enabled")
	}
	// In current implementation, we enable adapter to allow charging to full
	if !fake.adapter {
		t.Fatalf("expected adapter enabled")
	}

	// Simulate reaching full
	fake.charge = 100
	applyCalibrationWithinLoop(fake.charge)
	if calibrationState.Phase != calibration.PhaseHold {
		t.Fatalf("expected hold phase, got %s", calibrationState.Phase)
	}
	if calibrationState.HoldEndTime.IsZero() {
		t.Fatalf("HoldEndTime should be set")
	}

	// Fast-forward hold period
	calibrationState.HoldEndTime = time.Now().Add(-time.Second)
	applyCalibrationWithinLoop(fake.charge)
	if calibrationState.Phase != calibration.PhasePostHold {
		t.Fatalf("expected post-hold discharge phase, got %s", calibrationState.Phase)
	}

	// Simulate discharging back down to snapshot upper limit (original upper 80)
	fake.charge = 80
	applyCalibrationWithinLoop(fake.charge)
	if calibrationState.Phase != calibration.PhaseRestore {
		t.Fatalf("expected restore phase after post-hold discharge, got %s", calibrationState.Phase)
	}

	// Perform restore
	applyCalibrationWithinLoop(fake.charge)
	if calibrationState.Phase != calibration.PhaseIdle {
		t.Fatalf("expected idle at end, got %s", calibrationState.Phase)
	}
	if sleepCalls.prevent != 1 || sleepCalls.allow != 1 {
		t.Fatalf("sleep assertion calls = prevent:%d allow:%d, want 1/1", sleepCalls.prevent, sleepCalls.allow)
	}
}

func TestCalibrationFlowWithFirmwareChargeControl(t *testing.T) {
	sleepCalls := stubCalibrationSleep(t)
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
		value(smc.AdapterKey1, gosmc.TypeUInt8, 0),
	)
	if err := mock.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mock.Close() })
	if _, err := mock.EnsureFirmwareChargeLimit(78, 80); err != nil {
		t.Fatal(err)
	}

	previousSMC, previousConf, previousCapabilities := smcConn, conf, capabilities
	previousState, previousStatePath := calibrationState, calibrationStatePath
	previousIsAdapter := smcIsAdapterEnabled
	previousEnableAdapter, previousDisableAdapter := smcEnableAdapter, smcDisableAdapter
	t.Cleanup(func() {
		smcConn, conf, capabilities = previousSMC, previousConf, previousCapabilities
		calibrationState, calibrationStatePath = previousState, previousStatePath
		smcIsAdapterEnabled = previousIsAdapter
		smcEnableAdapter, smcDisableAdapter = previousEnableAdapter, previousDisableAdapter
	})
	smcConn = mock
	conf = &mockConf{upper: 80, lower: 78}
	capabilities = compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlFirmware,
		AdapterControl:    true,
		Calibration:       true,
	}
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	calibrationStatePath = ""
	smcIsAdapterEnabled = func() (bool, error) { return mock.IsAdapterEnabled() }
	smcEnableAdapter = func() error { return mock.EnableAdapter() }
	smcDisableAdapter = func() error { return mock.DisableAdapter() }

	if err := startCalibration(15, 10); err != nil {
		t.Fatal(err)
	}
	applyCalibrationWithinLoop(40)
	adapterEnabled, err := mock.IsAdapterEnabled()
	if err != nil {
		t.Fatal(err)
	}
	if adapterEnabled {
		t.Fatal("adapter should be disabled during discharge")
	}

	applyCalibrationWithinLoop(14)
	if calibrationState.Phase != calibration.PhaseCharge {
		t.Fatalf("phase = %s, want charge", calibrationState.Phase)
	}
	firmwareState, err := mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if firmwareState.Active {
		t.Fatal("firmware charge limit should be inactive while charging to full")
	}

	applyCalibrationWithinLoop(100)
	calibrationState.HoldEndTime = time.Now().Add(-time.Second)
	applyCalibrationWithinLoop(100)
	applyCalibrationWithinLoop(80)
	applyCalibrationWithinLoop(80)
	if calibrationState.Phase != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", calibrationState.Phase)
	}
	firmwareState, err = mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if !firmwareState.Active || firmwareState.Lower != 78 || firmwareState.Upper != 80 {
		t.Fatalf("firmware charge limit was not restored: %+v", firmwareState)
	}
	if sleepCalls.prevent != 1 || sleepCalls.allow != 1 {
		t.Fatalf("sleep assertion calls = prevent:%d allow:%d, want 1/1", sleepCalls.prevent, sleepCalls.allow)
	}
}
