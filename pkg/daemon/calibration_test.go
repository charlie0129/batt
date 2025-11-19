package daemon

import (
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/sirupsen/logrus"
)

// NOTE: These tests are simplified and mock minimal parts; smcConn and conf must be
// initialized externally for full integration. Here we focus on state transitions logic.

// mockConf implements the subset of Config used in calibration for test.
type mockConf struct {
	upper int
	lower int
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

// TestCalibrationFlow simulates the main phase transitions.
func TestCalibrationFlow(t *testing.T) {
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

	// Move charge below threshold to trigger charging phase
	fake.charge = 14
	applyCalibrationWithinLoop(fake.charge)
	if calibrationState.Phase != calibration.PhaseCharge {
		t.Fatalf("expected charge phase, got %s", calibrationState.Phase)
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
}
