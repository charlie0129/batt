package daemon

import (
	"math"

	"github.com/charlie0129/gosmc"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/powerui"
	"github.com/charlie0129/batt/pkg/smc"
)

// This file declares what the daemon needs from the Mac it runs on. Run wires
// the real SMC and IOKit implementations (see darwin.go and the iokit_*.go
// files); tests drive the daemon with a simulated firmware and fakes instead.

// SMC is the part of the Apple SMC the daemon drives. *smc.AppleSMC implements
// it. Which keys the firmware exposes decides the charge-control mode, so a
// test chooses a Mac by choosing the keys of its simulated SMC.
type SMC interface {
	Close() error
	Read(key string) (gosmc.Value, error)
	ChargeControlMode() compatibility.ChargeControlMode
	IsAdapterControlCapable() bool
	CheckMagSafeExistence() bool
	GetBatteryCharge() (int, error)
	IsPluggedIn() (bool, error)
	IsChargingEnabled() (bool, error)
	EnableCharging() error
	DisableCharging() error
	IsAdapterEnabled() (bool, error)
	EnableAdapter() error
	DisableAdapter() error
	GetFirmwareChargeLimit() (smc.FirmwareChargeLimit, error)
	EnsureFirmwareChargeLimit(lower, upper int) (bool, error)
	EnsureFirmwareChargeLimitDisabled() (bool, error)
	ResetChargeControl() error
	GetMagSafeLedState() (smc.MagSafeLedState, error)
	SetMagSafeLedState(state smc.MagSafeLedState) error
	DisableMagSafeLed() error
	SetMagSafeCharging(charging bool) error
}

// SleepSetting is the persistent SleepDisabled system power setting, the one
// `pmset -a disablesleep` changes. Unlike a power assertion it also suppresses
// lid-close sleep, and it outlives the process.
type SleepSetting interface {
	Disabled() (bool, error)
	SetDisabled(disabled bool) error
}

// SleepAssertions are the process-scoped IOPM power assertions the daemon
// takes. macOS drops them when the process exits.
type SleepAssertions interface {
	// PreventSleepOnAC keeps the system in dark wake while charging on AC.
	// Taking it twice is a no-op.
	PreventSleepOnAC() error
	// AllowSleepOnAC releases PreventSleepOnAC. Releasing twice is a no-op.
	AllowSleepOnAC() error
	// PreventCalibrationSleep prevents idle sleep on battery and AC while a
	// calibration runs. Forced sleep, such as closing the lid, still happens.
	PreventCalibrationSleep() error
	// AllowCalibrationSleep releases PreventCalibrationSleep.
	AllowCalibrationSleep() error
}

// SleepHandler receives the system power transitions the daemon reacts to.
type SleepHandler interface {
	// CanSystemSleep is asked before idle sleep. Returning false vetoes it.
	// macOS does not ask before forced sleep, such as closing the lid.
	CanSystemSleep() bool
	// SystemWillSleep runs right before any sleep, forced sleep included.
	SystemWillSleep()
	// SystemHasPoweredOn runs once the system has finished waking up.
	SystemHasPoweredOn()
}

// SleepNotifier delivers system power transitions to a SleepHandler.
type SleepNotifier interface {
	// Listen blocks and delivers notifications to h until Stop is called.
	Listen(h SleepHandler) error
	Stop()
}

// BatteryInfo reads battery telemetry the SMC does not expose.
type BatteryInfo interface {
	// Snapshot reads the battery and adapter state macOS reports.
	Snapshot() (BatterySnapshot, error)
	// FullChargeOverride reports whether macOS is charging to full once,
	// pausing its own charge limit.
	FullChargeOverride() (bool, error)
}

// BatterySnapshot is the battery and adapter state macOS reports through the
// AppleSmartBattery IORegistry entry. A number macOS does not report is 0.
type BatterySnapshot struct {
	Charging     bool
	FullyCharged bool
	Voltage      float64 // battery voltage in volts
	Amperage     float64 // battery current in amperes, negative while discharging
	CycleCount   int
	// DesignCapacity is what the battery held when new, in mAh.
	DesignCapacity int
	// MaxCapacity is what the battery holds when full today, in mAh.
	MaxCapacity int
	// InputVoltage and InputAmperage are what the Mac draws from the power
	// adapter, in volts and amperes.
	InputVoltage  float64
	InputAmperage float64
}

// BatteryPower is the power flowing into the battery in watts, negative while
// it discharges.
func (s BatterySnapshot) BatteryPower() float64 {
	return s.Voltage * s.Amperage
}

// AdapterPower is the power the Mac draws from the adapter in watts.
func (s BatterySnapshot) AdapterPower() float64 {
	return s.InputVoltage * s.InputAmperage
}

// SystemPower is the power the Mac itself uses in watts: what the adapter
// supplies, minus what goes into the battery or plus what comes out of it.
func (s BatterySnapshot) SystemPower() float64 {
	return max(0, s.AdapterPower()-s.BatteryPower())
}

// Health is the full-charge capacity as a rounded percentage of the design
// capacity, or 0 when the design capacity is unknown.
func (s BatterySnapshot) Health() int {
	if s.DesignCapacity <= 0 {
		return 0
	}
	return int(math.Round(float64(s.MaxCapacity) / float64(s.DesignCapacity) * 100))
}

// System is everything the daemon needs from the machine. Every field must be
// set.
type System struct {
	SMC         SMC
	NativeLimit powerui.Controller
	Sleep       SleepSetting
	Assertions  SleepAssertions
	Notifier    SleepNotifier
	Battery     BatteryInfo
}
