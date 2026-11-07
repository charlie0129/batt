package daemon

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charlie0129/gosmc"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerui"
	"github.com/charlie0129/batt/pkg/smc"
)

// This file is the test bench for the daemon: a catalog of simulated
// firmwares, an in-memory config, fakes for every system interface, and a
// fixture that wires them into a Daemon. Tests describe a Mac and a
// configuration, then drive the daemon through the same entry points macOS
// and the CLI use.

// ---------------------------------------------------------------------------
// Firmware catalog
// ---------------------------------------------------------------------------

// firmware describes which SMC keys a Mac exposes. Key presence is what
// decides the charge-control mode, so each entry stands for a kind of Mac.
type firmware struct {
	name string
	// chargeKeys are the charge-control keys present: the legacy CH0B/CH0C
	// pair, Tahoe's CHTE, or the bfF0/bfD0/bfE0 firmware limit.
	chargeKeys []string
	// adapterKey is the adapter-control key present, if any.
	adapterKey string
	// magSafe reports whether the MagSafe LED key is present.
	magSafe bool
}

var (
	// firmwareLegacy is an Apple Silicon Mac before macOS 26: direct charge
	// keys and the CH0I adapter key. batt runs the hysteresis loop itself.
	firmwareLegacy = firmware{name: "legacy", chargeKeys: []string{smc.ChargingKey1, smc.ChargingKey2}, adapterKey: smc.AdapterKey1, magSafe: true}
	// firmwareTahoe is macOS 26 firmware: the single CHTE charge key and the
	// CHIE adapter key.
	firmwareTahoe = firmware{name: "tahoe", chargeKeys: []string{smc.ChargingKey3}, adapterKey: smc.AdapterKey3, magSafe: true}
	// firmwareManaged is early macOS 27 firmware, which enforces the
	// configured limits itself through the bfF0/bfD0/bfE0 keys.
	firmwareManaged = firmware{name: "firmware-managed", chargeKeys: []string{smc.FirmwareChargeLimitActivationKey, smc.FirmwareChargeLimitUpperKey, smc.FirmwareChargeLimitLowerKey}, adapterKey: smc.AdapterKey1, magSafe: true}
	// firmwareGated is macOS 27 beta 4+ firmware: the charge keys need an
	// Apple-private entitlement, but the adapter key still works.
	firmwareGated = firmware{name: "gated", adapterKey: smc.AdapterKey3}
	// firmwareGatedNoAdapter has neither charge nor adapter keys, so only the
	// native macOS limit can remain.
	firmwareGatedNoAdapter = firmware{name: "gated-no-adapter"}
)

func (fw firmware) withMagSafe(present bool) firmware {
	fw.magSafe = present
	return fw
}

// machineState is the state of the simulated Mac when the daemon starts.
type machineState struct {
	charge    int
	pluggedIn bool
	// charging is whether the legacy charge keys allow charging.
	charging bool
	// adapterOn is whether wall power reaches the Mac.
	adapterOn bool
	led       smc.MagSafeLedState
	// firmwareLimit is the initial state of the firmware-managed limit.
	firmwareLimit smc.FirmwareChargeLimit
}

func defaultMachine() machineState {
	return machineState{charge: 50, pluggedIn: true, charging: true, adapterOn: true, led: smc.LEDSystem}
}

func mustValue(t *testing.T, key string, dt gosmc.DataType, data ...byte) gosmc.Value {
	t.Helper()
	v, err := gosmc.NewValue(key, dt, data)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// values builds the SMC contents of this firmware in state st.
func (fw firmware) values(t *testing.T, st machineState) []gosmc.Value {
	t.Helper()
	ac := byte(0)
	if st.pluggedIn {
		ac = 1
	}
	vals := []gosmc.Value{
		mustValue(t, smc.BatteryChargeKey, gosmc.TypeUInt8, byte(st.charge)),
		mustValue(t, smc.ACPowerKey, gosmc.TypeSInt8, ac),
	}
	for _, key := range fw.chargeKeys {
		switch key {
		case smc.ChargingKey1, smc.ChargingKey2:
			b := byte(0x02)
			if st.charging {
				b = 0x00
			}
			vals = append(vals, mustValue(t, key, gosmc.TypeUInt8, b))
		case smc.ChargingKey3:
			b := byte(0x01)
			if st.charging {
				b = 0x00
			}
			vals = append(vals, mustValue(t, key, gosmc.TypeUInt32, b, 0, 0, 0))
		case smc.FirmwareChargeLimitActivationKey:
			b := byte(0x00)
			if st.firmwareLimit.Active {
				b = 0x02
			}
			vals = append(vals, mustValue(t, key, gosmc.TypeUInt8, b))
		case smc.FirmwareChargeLimitUpperKey:
			vals = append(vals, mustValue(t, key, gosmc.TypeUInt32, byte(st.firmwareLimit.Upper), 0, 0, 0))
		case smc.FirmwareChargeLimitLowerKey:
			vals = append(vals, mustValue(t, key, gosmc.TypeUInt32, byte(st.firmwareLimit.Lower), 0, 0, 0))
		default:
			t.Fatalf("unknown charge key %q", key)
		}
	}
	if fw.adapterKey != "" {
		b := byte(0)
		if !st.adapterOn {
			b = 1
			if fw.adapterKey == smc.AdapterKey3 {
				b = 8
			}
		}
		vals = append(vals, mustValue(t, fw.adapterKey, gosmc.TypeUInt8, b))
	}
	if fw.magSafe {
		vals = append(vals, mustValue(t, smc.MagSafeLedKey, gosmc.TypeUInt8, byte(st.led)))
	}
	return vals
}

// ---------------------------------------------------------------------------
// Simulated SMC
// ---------------------------------------------------------------------------

// simSMC is the simulated firmware: a real smc.AppleSMC over gosmc's
// in-memory driver, so the daemon runs the same key decoding as on hardware,
// plus per-method fault injection and call counting.
type simSMC struct {
	*smc.AppleSMC
	fw     firmware
	mu     sync.Mutex
	faults map[string]error
	calls  map[string]int
}

func newSimSMC(t *testing.T, fw firmware, st machineState) *simSMC {
	t.Helper()
	conn := smc.NewMockValues(fw.values(t, st)...)
	if err := conn.Open(); err != nil {
		t.Fatal(err)
	}
	return &simSMC{AppleSMC: conn, fw: fw, faults: map[string]error{}, calls: map[string]int{}}
}

// fail makes method return err until cleared with fail(method, nil).
func (s *simSMC) fail(method string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.faults, method)
		return
	}
	s.faults[method] = err
}

// callCount reports how often the daemon called method.
func (s *simSMC) callCount(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

func (s *simSMC) enter(method string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[method]++
	return s.faults[method]
}

// Close records the call but keeps the simulated SMC readable, so a test can
// inspect what the daemon left behind after shutdown.
func (s *simSMC) Close() error {
	return s.enter("Close")
}

func (s *simSMC) Read(key string) (gosmc.Value, error) {
	if err := s.enter("Read"); err != nil {
		return gosmc.Value{}, err
	}
	return s.AppleSMC.Read(key)
}

func (s *simSMC) GetBatteryCharge() (int, error) {
	if err := s.enter("GetBatteryCharge"); err != nil {
		return 0, err
	}
	return s.AppleSMC.GetBatteryCharge()
}

func (s *simSMC) IsPluggedIn() (bool, error) {
	if err := s.enter("IsPluggedIn"); err != nil {
		return false, err
	}
	return s.AppleSMC.IsPluggedIn()
}

func (s *simSMC) IsChargingEnabled() (bool, error) {
	if err := s.enter("IsChargingEnabled"); err != nil {
		return false, err
	}
	return s.AppleSMC.IsChargingEnabled()
}

func (s *simSMC) EnableCharging() error {
	if err := s.enter("EnableCharging"); err != nil {
		return err
	}
	return s.AppleSMC.EnableCharging()
}

func (s *simSMC) DisableCharging() error {
	if err := s.enter("DisableCharging"); err != nil {
		return err
	}
	return s.AppleSMC.DisableCharging()
}

func (s *simSMC) IsAdapterEnabled() (bool, error) {
	if err := s.enter("IsAdapterEnabled"); err != nil {
		return false, err
	}
	return s.AppleSMC.IsAdapterEnabled()
}

func (s *simSMC) EnableAdapter() error {
	if err := s.enter("EnableAdapter"); err != nil {
		return err
	}
	return s.AppleSMC.EnableAdapter()
}

func (s *simSMC) DisableAdapter() error {
	if err := s.enter("DisableAdapter"); err != nil {
		return err
	}
	return s.AppleSMC.DisableAdapter()
}

func (s *simSMC) GetFirmwareChargeLimit() (smc.FirmwareChargeLimit, error) {
	if err := s.enter("GetFirmwareChargeLimit"); err != nil {
		return smc.FirmwareChargeLimit{}, err
	}
	return s.AppleSMC.GetFirmwareChargeLimit()
}

func (s *simSMC) EnsureFirmwareChargeLimit(lower, upper int) (bool, error) {
	if err := s.enter("EnsureFirmwareChargeLimit"); err != nil {
		return false, err
	}
	return s.AppleSMC.EnsureFirmwareChargeLimit(lower, upper)
}

func (s *simSMC) EnsureFirmwareChargeLimitDisabled() (bool, error) {
	if err := s.enter("EnsureFirmwareChargeLimitDisabled"); err != nil {
		return false, err
	}
	return s.AppleSMC.EnsureFirmwareChargeLimitDisabled()
}

func (s *simSMC) ResetChargeControl() error {
	if err := s.enter("ResetChargeControl"); err != nil {
		return err
	}
	return s.AppleSMC.ResetChargeControl()
}

func (s *simSMC) GetMagSafeLedState() (smc.MagSafeLedState, error) {
	if err := s.enter("GetMagSafeLedState"); err != nil {
		return smc.LEDOrange, err
	}
	return s.AppleSMC.GetMagSafeLedState()
}

func (s *simSMC) SetMagSafeLedState(state smc.MagSafeLedState) error {
	if err := s.enter("SetMagSafeLedState"); err != nil {
		return err
	}
	return s.AppleSMC.SetMagSafeLedState(state)
}

func (s *simSMC) DisableMagSafeLed() error {
	if err := s.enter("DisableMagSafeLed"); err != nil {
		return err
	}
	return s.AppleSMC.DisableMagSafeLed()
}

func (s *simSMC) SetMagSafeCharging(charging bool) error {
	if err := s.enter("SetMagSafeCharging"); err != nil {
		return err
	}
	return s.AppleSMC.SetMagSafeCharging(charging)
}

// The helpers below change or inspect the simulated hardware directly. They
// bypass fault injection and call counting.

func (s *simSMC) setCharge(t *testing.T, percent int) {
	t.Helper()
	if err := s.Write(smc.BatteryChargeKey, []byte{byte(percent)}); err != nil {
		t.Fatal(err)
	}
}

func (s *simSMC) setPluggedIn(t *testing.T, pluggedIn bool) {
	t.Helper()
	b := byte(0)
	if pluggedIn {
		b = 1
	}
	if err := s.Write(smc.ACPowerKey, []byte{b}); err != nil {
		t.Fatal(err)
	}
}

func (s *simSMC) setAdapter(t *testing.T, on bool) {
	t.Helper()
	var err error
	if on {
		err = s.AppleSMC.EnableAdapter()
	} else {
		err = s.AppleSMC.DisableAdapter()
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (s *simSMC) setCharging(t *testing.T, on bool) {
	t.Helper()
	var err error
	if on {
		err = s.AppleSMC.EnableCharging()
	} else {
		err = s.AppleSMC.DisableCharging()
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (s *simSMC) chargingEnabled(t *testing.T) bool {
	t.Helper()
	on, err := s.AppleSMC.IsChargingEnabled()
	if err != nil {
		t.Fatal(err)
	}
	return on
}

func (s *simSMC) adapterEnabled(t *testing.T) bool {
	t.Helper()
	on, err := s.AppleSMC.IsAdapterEnabled()
	if err != nil {
		t.Fatal(err)
	}
	return on
}

// led returns the raw MagSafe LED register.
func (s *simSMC) led(t *testing.T) smc.MagSafeLedState {
	t.Helper()
	v, err := s.AppleSMC.Read(smc.MagSafeLedKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Bytes) != 1 {
		t.Fatalf("LED register has %d bytes", len(v.Bytes))
	}
	return smc.MagSafeLedState(v.Bytes[0])
}

func (s *simSMC) firmwareLimit(t *testing.T) smc.FirmwareChargeLimit {
	t.Helper()
	limit, err := s.AppleSMC.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	return limit
}

// ---------------------------------------------------------------------------
// System fakes
// ---------------------------------------------------------------------------

// fakeSleepSetting replaces the IOKit SleepDisabled setting with an in-memory
// value and records how often it was actually written.
type fakeSleepSetting struct {
	mu            sync.Mutex
	value         bool
	writes        int
	writtenValues []bool
	getErr        error
	setErr        error
	setErrFor     *bool // fail only when writing this value
}

func (f *fakeSleepSetting) Disabled() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return false, f.getErr
	}
	return f.value, nil
}

func (f *fakeSleepSetting) SetDisabled(disabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil && (f.setErrFor == nil || *f.setErrFor == disabled) {
		return f.setErr
	}
	f.value = disabled
	f.writes++
	f.writtenValues = append(f.writtenValues, disabled)
	return nil
}

func (f *fakeSleepSetting) disabled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.value
}

func (f *fakeSleepSetting) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

// fakeAssertions records the IOPM power assertions the daemon holds.
type fakeAssertions struct {
	mu                 sync.Mutex
	foreign            []SleepAssertion
	foreignErr         error
	onAC               bool
	calibration        bool
	preventOnAC        int
	allowOnAC          int
	preventCalibration int
	allowCalibration   int
	err                error
}

func (f *fakeAssertions) PreventSleepOnAC() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.preventOnAC++
	f.onAC = true
	return nil
}

func (f *fakeAssertions) AllowSleepOnAC() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.allowOnAC++
	f.onAC = false
	return nil
}

func (f *fakeAssertions) PreventCalibrationSleep() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.preventCalibration++
	f.calibration = true
	return nil
}

func (f *fakeAssertions) AllowCalibrationSleep() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.allowCalibration++
	f.calibration = false
	return nil
}

func (f *fakeAssertions) ForeignSleepAssertions() ([]SleepAssertion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SleepAssertion(nil), f.foreign...), f.foreignErr
}

func (f *fakeAssertions) holdsSleepOnAC() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.onAC
}

func (f *fakeAssertions) holdsCalibrationSleep() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calibration
}

// fakeNotifier records the handler the daemon registers and lets tests
// deliver sleep transitions to it.
type fakeNotifier struct {
	mu      sync.Mutex
	handler SleepHandler
	stop    chan struct{}
	stopped bool
}

func newFakeNotifier() *fakeNotifier { return &fakeNotifier{stop: make(chan struct{})} }

func (f *fakeNotifier) Listen(h SleepHandler) error {
	f.mu.Lock()
	f.handler = h
	f.mu.Unlock()
	<-f.stop
	return nil
}

func (f *fakeNotifier) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.stopped {
		f.stopped = true
		close(f.stop)
	}
}

// fakeBatteryInfo stands in for IOKit battery telemetry.
type fakeBatteryInfo struct {
	mu          sync.Mutex
	snapshot    BatterySnapshot
	err         error
	override    bool
	overrideErr error
	polls       int
}

func (f *fakeBatteryInfo) Snapshot() (BatterySnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	return f.snapshot, f.err
}

func (f *fakeBatteryInfo) FullChargeOverride() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.override, f.overrideErr
}

func (f *fakeBatteryInfo) setCharging(charging bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshot = batteryCharging(charging)
}

func (f *fakeBatteryInfo) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeBatteryInfo) pollCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.polls
}

// batteryCharging is a battery snapshot whose only interesting field is
// whether the battery is charging.
func batteryCharging(charging bool) BatterySnapshot {
	return BatterySnapshot{Charging: charging}
}

// fakeNativeLimit stands in for PowerUIAgent.
type fakeNativeLimit struct {
	supported  bool
	limits     []int
	limit      int
	enabled    bool
	registered bool
	exact      bool
	err        error
	setCalls   []int
	disables   int
	// deferRegistration leaves new targets pending, as macOS does while the
	// battery charges. pending is the target it has yet to apply.
	deferRegistration bool
	pending           int
	noDrainLimit      int
	noDrainEnabled    bool
	noDrainSets       []int
	noDrainDisables   int
	noDrainErr        error
	noDrainSetErr     error
	noDrainReloads    int
	noDrainReloadErr  error
}

func (f *fakeNativeLimit) Supported() bool            { return f.supported }
func (f *fakeNativeLimit) SupportsExactTargets() bool { return f.exact }

func (f *fakeNativeLimit) AvailableLimits() ([]int, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]int(nil), f.limits...), nil
}

func (f *fakeNativeLimit) Limit() (int, bool, error) {
	if f.err != nil {
		return 0, false, f.err
	}
	if f.pending != 0 {
		return 0, true, nil
	}
	return f.limit, f.enabled, nil
}

func (f *fakeNativeLimit) SetLimit(limit int) error {
	if f.err != nil {
		return f.err
	}
	f.setCalls = append(f.setCalls, limit)
	if f.deferRegistration && limit < 100 {
		f.pending = limit
		return fmt.Errorf("native charge target %d%%: %w", limit, powerui.ErrRegistrationPending)
	}
	f.pending = 0
	f.limit = limit
	f.enabled = limit < 100
	return nil
}

// register applies the pending target, as macOS does once the battery level
// changes.
func (f *fakeNativeLimit) register() {
	f.limit, f.enabled, f.pending, f.deferRegistration = f.pending, true, 0, false
}

func (f *fakeNativeLimit) Disable() error {
	if f.err != nil {
		return f.err
	}
	if !f.enabled && !f.registered && f.pending == 0 {
		return nil
	}
	f.disables++
	f.limit = 100
	f.enabled = false
	f.registered = false
	f.pending = 0
	return nil
}

func (f *fakeNativeLimit) NoDrainLimit() (int, bool, error) {
	if f.noDrainErr != nil {
		return 100, false, f.noDrainErr
	}
	if !f.exact {
		return 100, false, powerui.ErrNoDrainUnsupported
	}
	if !f.noDrainEnabled {
		return 100, false, nil
	}
	return f.noDrainLimit, true, nil
}

func (f *fakeNativeLimit) SetNoDrainLimit(limit int) error {
	if f.noDrainErr != nil {
		return f.noDrainErr
	}
	if f.noDrainSetErr != nil {
		return f.noDrainSetErr
	}
	f.noDrainSets = append(f.noDrainSets, limit)
	// The real controller disables PowerUIAgent before saving powerd's policy.
	_ = f.Disable()
	f.noDrainLimit, f.noDrainEnabled = limit, true
	return f.noDrainReloadErr
}

func (f *fakeNativeLimit) DisableNoDrain() error {
	if f.noDrainErr != nil {
		return f.noDrainErr
	}
	f.noDrainDisables++
	f.noDrainLimit, f.noDrainEnabled = 100, false
	return f.noDrainReloadErr
}

func (f *fakeNativeLimit) ReloadNoDrain() error {
	f.noDrainReloads++
	return f.noDrainReloadErr
}

// sliderNative is a macOS 26-style native backend offering slider values only.
func sliderNative(limits ...int) *fakeNativeLimit {
	return &fakeNativeLimit{supported: true, limits: limits, limit: 100}
}

// exactNative is a macOS 27-style native backend accepting exact targets.
func exactNative() *fakeNativeLimit {
	limits := make([]int, 91)
	for i := range limits {
		limits[i] = 10 + i
	}
	return &fakeNativeLimit{supported: true, exact: true, limits: limits, limit: 100}
}

// ---------------------------------------------------------------------------
// In-memory config
// ---------------------------------------------------------------------------

// memConfigState holds every setting so tests can compare before and after.
type memConfigState struct {
	upper                        int
	lower                        int
	preventIdleSleep             bool
	disableChargingPreSleep      bool
	preventSystemSleep           bool
	preventSleepOnAdapterDisable bool
	allowNonRootAccess           bool
	adapterMode                  bool
	noDrain                      bool
	magSafe                      config.ControlMagSafeMode
	calibrationThreshold         int
	calibrationHoldMinutes       int
	cron                         string
	disableUntil                 time.Time
	preDisableLimit              int
	adapterDisableUntil          time.Time
	chargeOnceTarget             int
}

// memConfig is a config.Config that lives in memory. Unlike config.File it
// stores exactly what it is given and can fail to save on demand.
type memConfig struct {
	mu sync.Mutex
	memConfigState
	saveErr error
	loadErr error
	saves   int
}

var _ config.Config = (*memConfig)(nil)

func newMemConfig() *memConfig {
	return &memConfig{memConfigState: memConfigState{
		upper:                  80,
		lower:                  78,
		magSafe:                config.ControlMagSafeModeDisabled,
		calibrationThreshold:   15,
		calibrationHoldMinutes: 120,
	}}
}

// snapshot returns the settings for comparison.
func (m *memConfig) snapshot() memConfigState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.memConfigState
}

func (m *memConfig) set(fn func(s *memConfigState)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(&m.memConfigState)
}

func (m *memConfig) UpperLimit() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.upper
}

func (m *memConfig) LowerLimit() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lower
}

func (m *memConfig) PreventIdleSleep() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.preventIdleSleep
}

func (m *memConfig) DisableChargingPreSleep() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.disableChargingPreSleep
}

func (m *memConfig) PreventSystemSleep() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.preventSystemSleep
}

func (m *memConfig) PreventSleepOnAdapterDisable() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.preventSleepOnAdapterDisable
}

func (m *memConfig) AllowNonRootAccess() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.allowNonRootAccess
}

func (m *memConfig) AdapterMode() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.adapterMode
}

func (m *memConfig) NoDrain() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.noDrain
}

func (m *memConfig) ControlMagSafeLED() config.ControlMagSafeMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.magSafe
}

func (m *memConfig) CalibrationDischargeThreshold() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calibrationThreshold
}

func (m *memConfig) CalibrationHoldDurationMinutes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calibrationHoldMinutes
}

func (m *memConfig) Cron() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cron
}

func (m *memConfig) DisableUntil() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.disableUntil
}

func (m *memConfig) PreDisableLimit() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.preDisableLimit
}

func (m *memConfig) AdapterDisableUntil() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.adapterDisableUntil
}

func (m *memConfig) ChargeOnceTarget() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.chargeOnceTarget
}

func (m *memConfig) SetUpperLimit(i int) { m.set(func(s *memConfigState) { s.upper = i }) }
func (m *memConfig) SetLowerLimit(i int) { m.set(func(s *memConfigState) { s.lower = i }) }
func (m *memConfig) SetPreventIdleSleep(b bool) {
	m.set(func(s *memConfigState) { s.preventIdleSleep = b })
}
func (m *memConfig) SetDisableChargingPreSleep(b bool) {
	m.set(func(s *memConfigState) { s.disableChargingPreSleep = b })
}
func (m *memConfig) SetPreventSystemSleep(b bool) {
	m.set(func(s *memConfigState) { s.preventSystemSleep = b })
}
func (m *memConfig) SetPreventSleepOnAdapterDisable(b bool) {
	m.set(func(s *memConfigState) { s.preventSleepOnAdapterDisable = b })
}
func (m *memConfig) SetAllowNonRootAccess(b bool) {
	m.set(func(s *memConfigState) { s.allowNonRootAccess = b })
}
func (m *memConfig) SetAdapterMode(b bool) { m.set(func(s *memConfigState) { s.adapterMode = b }) }
func (m *memConfig) SetNoDrain(b bool)     { m.set(func(s *memConfigState) { s.noDrain = b }) }
func (m *memConfig) SetControlMagSafeLED(mode config.ControlMagSafeMode) {
	m.set(func(s *memConfigState) { s.magSafe = mode })
}
func (m *memConfig) SetCron(cron string) { m.set(func(s *memConfigState) { s.cron = cron }) }
func (m *memConfig) SetCalibrationDischargeThreshold(i int) {
	m.set(func(s *memConfigState) { s.calibrationThreshold = i })
}
func (m *memConfig) SetCalibrationHoldDurationMinutes(i int) {
	m.set(func(s *memConfigState) { s.calibrationHoldMinutes = i })
}
func (m *memConfig) SetDisableTimer(until time.Time, prevLimit int) {
	m.set(func(s *memConfigState) { s.disableUntil, s.preDisableLimit = until, prevLimit })
}
func (m *memConfig) ClearDisableTimer() {
	m.set(func(s *memConfigState) { s.disableUntil, s.preDisableLimit = time.Time{}, 0 })
}
func (m *memConfig) SetAdapterDisableTimer(until time.Time) {
	m.set(func(s *memConfigState) { s.adapterDisableUntil = until })
}
func (m *memConfig) ClearAdapterDisableTimer() {
	m.set(func(s *memConfigState) { s.adapterDisableUntil = time.Time{} })
}
func (m *memConfig) SetChargeOnceTarget(target int) {
	m.set(func(s *memConfigState) { s.chargeOnceTarget = target })
}
func (m *memConfig) ClearChargeOnceTarget() {
	m.set(func(s *memConfigState) { s.chargeOnceTarget = 0 })
}

func (m *memConfig) LogValue() slog.Value { return slog.GroupValue() }

func (m *memConfig) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadErr
}

func (m *memConfig) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saves++
	return nil
}

func (m *memConfig) failSave(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveErr = err
}

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

type fixtureOptions struct {
	machine       machineState
	conf          config.Config
	fileConf      bool
	native        *fakeNativeLimit
	sleepDisabled bool
	caps          *compatibility.Capabilities
	noDetect      bool
}

type fixtureOption func(*fixtureOptions)

func withCharge(percent int) fixtureOption {
	return func(o *fixtureOptions) { o.machine.charge = percent }
}

func withPluggedIn(pluggedIn bool) fixtureOption {
	return func(o *fixtureOptions) { o.machine.pluggedIn = pluggedIn }
}

// withCharging sets whether the legacy charge keys allow charging at start.
func withCharging(charging bool) fixtureOption {
	return func(o *fixtureOptions) { o.machine.charging = charging }
}

// withAdapter sets whether wall power reaches the Mac at start.
func withAdapter(on bool) fixtureOption {
	return func(o *fixtureOptions) { o.machine.adapterOn = on }
}

func withLED(state smc.MagSafeLedState) fixtureOption {
	return func(o *fixtureOptions) { o.machine.led = state }
}

func withFirmwareLimit(active bool, lower, upper int) fixtureOption {
	return func(o *fixtureOptions) {
		o.machine.firmwareLimit = smc.FirmwareChargeLimit{Active: active, Lower: uint32(lower), Upper: uint32(upper)}
	}
}

// withFileConfig uses a real config.File in a temporary directory instead of
// the in-memory config, for tests that check what is persisted on disk.
func withFileConfig() fixtureOption {
	return func(o *fixtureOptions) { o.fileConf = true }
}

func withConfig(c config.Config) fixtureOption {
	return func(o *fixtureOptions) { o.conf = c }
}

// withNative installs a PowerUIAgent stand-in. The default is unsupported,
// as on Macs without a native charge limit.
func withNative(n *fakeNativeLimit) fixtureOption {
	return func(o *fixtureOptions) { o.native = n }
}

// withSleepDisabled sets the SleepDisabled system setting the daemon finds.
func withSleepDisabled(disabled bool) fixtureOption {
	return func(o *fixtureOptions) { o.sleepDisabled = disabled }
}

// withCapabilities installs caps instead of detecting them from the firmware.
func withCapabilities(caps compatibility.Capabilities) fixtureOption {
	return func(o *fixtureOptions) { o.caps = &caps }
}

// withoutDetection leaves the daemon as New returns it, before start.
func withoutDetection() fixtureOption {
	return func(o *fixtureOptions) { o.noDetect = true }
}

// fixture is a daemon wired to a simulated Mac.
type fixture struct {
	t        *testing.T
	d        *Daemon
	conf     config.Config
	mem      *memConfig   // nil with withFileConfig or withConfig
	file     *config.File // only with withFileConfig
	path     string       // the config file, only with withFileConfig
	smc      *simSMC
	native   *fakeNativeLimit
	sleep    *fakeSleepSetting
	asserts  *fakeAssertions
	notifier *fakeNotifier
	battery  *fakeBatteryInfo
	dir      string
}

// newFixture builds a daemon for a Mac with firmware fw. Capabilities are
// detected from the firmware unless an option says otherwise, so the daemon
// is in the state start leaves it in, minus the persisted-state reconciliation.
func newFixture(t *testing.T, fw firmware, opts ...fixtureOption) *fixture {
	t.Helper()
	o := fixtureOptions{machine: defaultMachine()}
	for _, opt := range opts {
		opt(&o)
	}

	f := &fixture{t: t, dir: t.TempDir()}
	f.smc = newSimSMC(t, fw, o.machine)
	f.native = o.native
	if f.native == nil {
		f.native = &fakeNativeLimit{limit: 100}
	}
	f.sleep = &fakeSleepSetting{value: o.sleepDisabled}
	f.asserts = &fakeAssertions{}
	f.notifier = newFakeNotifier()
	f.battery = &fakeBatteryInfo{snapshot: batteryCharging(o.machine.charging && o.machine.pluggedIn)}

	switch {
	case o.conf != nil:
		f.conf = o.conf
		if m, ok := o.conf.(*memConfig); ok {
			f.mem = m
		}
	case o.fileConf:
		f.path = filepath.Join(f.dir, "batt.json")
		file, err := config.NewFile(f.path)
		if err != nil {
			t.Fatal(err)
		}
		f.file = file
		f.conf = file
	default:
		f.mem = newMemConfig()
		f.conf = f.mem
	}

	f.d = New(f.conf, System{
		SMC:         f.smc,
		NativeLimit: f.native,
		Sleep:       f.sleep,
		Assertions:  f.asserts,
		Notifier:    f.notifier,
		Battery:     f.battery,
	})
	f.d.euid = func() int { return 0 }
	f.d.preSleepLoopDelay = 20 * time.Millisecond
	f.d.postSleepLoopDelay = 20 * time.Millisecond
	f.d.calibrationStatePath = filepath.Join(f.dir, "batt.state.json")
	if err := f.d.holds.init(filepath.Join(f.dir, "batt.sleep.json")); err != nil {
		t.Fatal(err)
	}

	switch {
	case o.caps != nil:
		f.d.setChargeControl(*o.caps)
	case !o.noDetect:
		f.d.setChargeControl(f.d.detectCapabilities())
	}
	return f
}

// serve sends one request to the daemon's API. A non-empty body is JSON.
func (f *fixture) serve(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	request := httptest.NewRequest(method, path, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	f.d.routes().ServeHTTP(response, request)
	return response
}

// mustServe is serve that fails the test unless the status is wantCode.
func (f *fixture) mustServe(method, path, body string, wantCode int) *httptest.ResponseRecorder {
	f.t.Helper()
	response := f.serve(method, path, body)
	if response.Code != wantCode {
		f.t.Fatalf("%s %s: status = %d, want %d; body: %s", method, path, response.Code, wantCode, response.Body.String())
	}
	return response
}

func (f *fixture) put(path, body string) *httptest.ResponseRecorder {
	return f.serve(http.MethodPut, path, body)
}

func (f *fixture) post(path string) *httptest.ResponseRecorder {
	return f.serve(http.MethodPost, path, "")
}

func (f *fixture) caps() compatibility.Capabilities { return f.d.getCapabilities() }

func (f *fixture) setCaps(caps compatibility.Capabilities) { f.d.setChargeControl(caps) }

// setCalibration replaces the calibration state.
func (f *fixture) setCalibration(st *calibration.State) {
	f.d.calibrationMu.Lock()
	defer f.d.calibrationMu.Unlock()
	f.d.calibrationState = st
}

func (f *fixture) calibration() *calibration.State {
	f.d.calibrationMu.Lock()
	defer f.d.calibrationMu.Unlock()
	return f.d.calibrationState
}

func (f *fixture) phase() calibration.Phase { return f.d.calibrationPhase() }

func (f *fixture) sleepPath() string { return f.d.holds.path }

// settleLoop records a stretch of on-time maintain loops, as a daemon that
// has been running undisturbed has. Without it the daemon treats the missing
// records as missed loops, as it does right after a sleep.
func (f *fixture) settleLoop() {
	now := time.Now()
	for i := 12; i >= 0; i-- {
		f.d.loopRecorder.AddRecord(now.Add(-time.Duration(i) * f.d.loopInterval))
	}
}

func (f *fixture) holdsSleep(reason string) bool { return f.d.holds.holding(reason) }

// reloadFile reads the config file back from disk.
func (f *fixture) reloadFile() *config.File {
	f.t.Helper()
	if f.path == "" {
		f.t.Fatal("reloadFile needs withFileConfig")
	}
	reloaded, err := config.NewFile(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	return reloaded
}

// Capability sets for tests that install capabilities directly.

func legacyCaps() compatibility.Capabilities {
	return compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlLegacy,
		SleepHooks:        true,
		MagSafeLED:        true,
		AdapterControl:    true,
		Calibration:       true,
	}
}

func adapterCaps() compatibility.Capabilities {
	return compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlAdapter,
		SleepHooks:        true,
	}
}

func nativeCaps(limits ...int) compatibility.Capabilities {
	return compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlNative,
		SupportedLimits:   limits,
		AdapterControl:    true,
		Calibration:       true,
	}
}
